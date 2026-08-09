package scheduler

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"uts_bot/internal/config"
	"uts_bot/internal/coursestatic"
	"uts_bot/internal/moodlehttp"
	"uts_bot/internal/saia"
	"uts_bot/internal/store"
)

// perCourseWorkAllowance is how long one course's crawl may take before the cycle is treated as
// wedged: a course page, every activity page linked from it, and every attachment download and
// parse. It only feeds the deadline calculation; a healthy course finishes well inside it.
const perCourseWorkAllowance = 5 * time.Minute

// Scheduler runs the Moodle scrape on a timer so the database changes on its own, which is what
// lets uts_notifier work purely from the outbox instead of poking uts_bot's HTTP API.
type Scheduler struct {
	db *sql.DB
	// mu is the same lock the API controller holds around its scrapers. Sharing it keeps a
	// scheduled crawl and a manual request from running two Moodle logins at once.
	mu *sync.Mutex

	wg   sync.WaitGroup
	stop context.CancelFunc
}

// Start launches the scrape loop and returns immediately. The returned Scheduler must be closed
// with Stop to wait for an in-flight cycle. A nil result means scheduling is disabled.
func Start(ctx context.Context, db *sql.DB, mu *sync.Mutex) *Scheduler {
	if !config.ScrapeEnabled {
		slog.Info("scrape scheduler disabled", "reason", "SCRAPE_ENABLED=false")
		return nil
	}
	if db == nil {
		slog.Warn("scrape scheduler disabled", "reason", "no database configured")
		return nil
	}
	if mu == nil {
		mu = &sync.Mutex{}
	}

	loopCtx, cancel := context.WithCancel(ctx)
	s := &Scheduler{db: db, mu: mu, stop: cancel}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.loop(loopCtx)
	}()
	return s
}

// Stop cancels the loop and waits for the current cycle to unwind.
func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.stop()
	s.wg.Wait()
}

// loop rests, then scrapes, then rests again. Resting *after* the previous cycle finishes rather
// than firing on a fixed phase means a long cycle can never overlap the next one.
func (s *Scheduler) loop(ctx context.Context) {
	slog.Info("scrape scheduler started",
		"startup_delay", config.ScrapeStartupDelay,
		"rest_between_cycles", config.ScrapeInterval,
		"rest_between_courses", config.ScrapeCourseRest,
		"rest_between_phases", config.ScrapePhaseRest,
		"cycle_deadline", cycleDeadline(),
		"active_hours", [2]int{config.ScrapeActiveStartHour, config.ScrapeActiveEndHour},
		"outbox_retention_days", config.OutboxRetentionDays,
	)

	// Only the first rest is the startup delay; from then on it is the between-cycle interval.
	rest := config.ScrapeStartupDelay
	for {
		if !sleepCtx(ctx, rest) {
			slog.Info("scrape scheduler stopped")
			return
		}
		rest = config.ScrapeInterval

		if !withinActiveHours(time.Now()) {
			slog.Debug("skipping scrape cycle outside active hours")
			continue
		}
		s.runCycle(ctx)
		s.pruneOutbox(ctx)
	}
}

// withinActiveHours reports whether the local hour falls in [start, end).
func withinActiveHours(now time.Time) bool {
	h := now.Hour()
	return h >= config.ScrapeActiveStartHour && h < config.ScrapeActiveEndHour
}

// sleepCtx pauses for d, returning false if ctx was cancelled first. Every pause goes through
// this so a shutdown is never stuck waiting out a multi-minute rest.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// cycleDeadline budgets every rest in a cycle plus a working allowance for each course, in both
// the activities and grades phases. Deriving it means raising a rest cannot silently start
// truncating cycles.
func cycleDeadline() time.Duration {
	n := time.Duration(len(coursestatic.UFTMoodleCourses))
	if n == 0 {
		return perCourseWorkAllowance
	}
	rests := (n-1)*config.ScrapeCourseRest + config.ScrapePhaseRest
	return rests + 2*n*perCourseWorkAllowance
}

// runCycle walks every course for activities, rests, then collects grade reports. All detected
// changes are published in a single transaction at the end so the notifier sees the cycle as one
// atomic batch and sends one grouped message.
func (s *Scheduler) runCycle(ctx context.Context) {
	cycleCtx, cancel := context.WithTimeout(ctx, cycleDeadline())
	defer cancel()

	started := time.Now()
	sc := saia.New(moodlehttp.New())
	sc.DB = s.db

	// Publish on every exit path, including a cancelled or timed-out cycle: the scraped rows are
	// already committed, so dropping their events would lose those notifications for good.
	defer func() { s.publish(ctx, sc.TakeEvents(), started) }()

	if !s.scrapeActivities(cycleCtx, sc) {
		return
	}
	if !sleepCtx(cycleCtx, config.ScrapePhaseRest) {
		return
	}
	s.scrapeGrades(cycleCtx, sc)
}

// scrapeActivities crawls one course at a time, resting between them. It deliberately avoids
// saia.Run's all-courses mode so the requests are spread out instead of arriving back to back.
// Returns false if the cycle was cut short.
func (s *Scheduler) scrapeActivities(ctx context.Context, sc *saia.SAIA) bool {
	for i, course := range coursestatic.UFTMoodleCourses {
		if i > 0 && !sleepCtx(ctx, config.ScrapeCourseRest) {
			slog.Warn("scrape cycle ended during activities", "completed_courses", i)
			return false
		}
		courseViewID := course.MoodleID
		s.underScrapeLock(func() {
			slog.Info("scraping course activities", "course", course.Name, "moodle_id", courseViewID)
			if err := sc.Run(ctx, config.SAIAPage, &courseViewID); err != nil {
				slog.Error("scheduled activities scrape failed", "course", course.Name, "err", err)
			}
		})
	}
	return true
}

// scrapeGrades collects every course's grade report back to back under a single lock hold. Grade
// reports are two cheap page loads each, so they do not need the pacing the activity crawl does.
func (s *Scheduler) scrapeGrades(ctx context.Context, sc *saia.SAIA) {
	s.underScrapeLock(func() {
		for _, course := range coursestatic.UFTMoodleCourses {
			if ctx.Err() != nil {
				slog.Warn("scrape cycle ended during grades", "course", course.Name)
				return
			}
			if err := sc.PersistCourseGrades(ctx, course.Name, course.MoodleID); err != nil {
				slog.Error("scheduled grades scrape failed", "course", course.Name, "err", err)
			}
		}
	})
}

// underScrapeLock runs fn holding the shared Moodle lock. The lock is taken per crawl rather than
// once per cycle because a cycle now spends most of its time resting: holding it throughout would
// stall any manual API scrape for the better part of an hour.
func (s *Scheduler) underScrapeLock(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

func (s *Scheduler) publish(ctx context.Context, events []store.OutboxEvent, started time.Time) {
	if len(events) == 0 {
		slog.Info("scrape cycle finished with no changes", "took", time.Since(started))
		return
	}
	// Detached from the cycle context so a cycle that ran out its deadline still queues what it
	// already found.
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	if err := store.InsertOutboxEvents(publishCtx, s.db, events); err != nil {
		slog.Error("insert outbox events", "count", len(events), "err", err)
		return
	}
	slog.Info("scrape cycle finished", "events", len(events), "took", time.Since(started))
}

func (s *Scheduler) pruneOutbox(ctx context.Context) {
	pruneCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	n, err := store.PruneConsumedOutboxEvents(pruneCtx, s.db, config.OutboxRetentionDays)
	if err != nil {
		slog.Error("prune outbox", "err", err)
		return
	}
	if n > 0 {
		slog.Info("pruned delivered outbox events", "count", n)
	}
}
