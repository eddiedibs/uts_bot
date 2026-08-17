package scheduler

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"uts_bot/internal/config"
	"uts_bot/internal/moodlehttp"
	"uts_bot/internal/saia"
	"uts_bot/internal/store"
)

// perCourseWorkAllowance is how long one course's crawl may take before the cycle is treated as
// wedged: a course page, every activity page linked from it, and every attachment download and
// parse. It only feeds the deadline calculation; a healthy course finishes well inside it.
const perCourseWorkAllowance = 5 * time.Minute

// discoveryDeadline bounds how long finding the live course list may take, separate from the
// per-course crawl budget in cycleDeadline.
const discoveryDeadline = 2 * time.Minute

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
// truncating cycles. n is the course count for this cycle, discovered fresh each time.
func cycleDeadline(n int) time.Duration {
	if n == 0 {
		return perCourseWorkAllowance
	}
	nn := time.Duration(n)
	rests := (nn-1)*config.ScrapeCourseRest + config.ScrapePhaseRest
	return rests + 2*nn*perCourseWorkAllowance
}

// runCycle discovers the live course list, walks every course for activities, rests, then
// collects grade reports. All detected changes are published in a single transaction at the end
// so the notifier sees the cycle as one atomic batch and sends one grouped message.
func (s *Scheduler) runCycle(ctx context.Context) {
	started := time.Now()
	sc := saia.New(moodlehttp.New())
	sc.DB = s.db

	// Publish on every exit path, including a cancelled or timed-out cycle: the scraped rows are
	// already committed, so dropping their events would lose those notifications for good.
	defer func() { s.publish(ctx, sc.TakeEvents(), started) }()

	courses := s.discoverCourses(ctx, sc)
	if len(courses) == 0 {
		slog.Warn("scrape cycle skipped: no courses to crawl")
		return
	}

	cycleCtx, cancel := context.WithTimeout(ctx, cycleDeadline(len(courses)))
	defer cancel()

	if !s.scrapeActivities(cycleCtx, sc, courses) {
		return
	}
	if !sleepCtx(cycleCtx, config.ScrapePhaseRest) {
		return
	}
	s.scrapeGrades(cycleCtx, sc, courses)
}

// discoverCourses logs in, finds the live course list on the Moodle dashboard, and syncs it into
// the DB: newly enrolled courses are added, and ones no longer visible (term ended, unenrolled)
// are deleted along with their activities and grades. On failure it falls back to the last-known
// DB rows so a transient Moodle hiccup does not stall the whole cycle.
func (s *Scheduler) discoverCourses(ctx context.Context, sc *saia.SAIA) []store.Course {
	discoverCtx, cancel := context.WithTimeout(ctx, discoveryDeadline)
	defer cancel()

	var courses []store.Course
	s.underScrapeLock(func() {
		discovered, err := sc.DiscoverCourses(discoverCtx, config.SAIAPage)
		if err != nil {
			slog.Error("course discovery failed, using last known courses", "err", err)
			courses, err = store.ListCourses(ctx, s.db)
			if err != nil {
				slog.Error("list courses fallback", "err", err)
			}
			return
		}
		courses = discovered
		s.syncCourses(ctx, discovered)
	})
	return courses
}

// syncCourses commits discovered as the new course set. Failures are logged, not fatal: the
// scrape still proceeds against the freshly discovered list even if the DB write did not stick.
func (s *Scheduler) syncCourses(ctx context.Context, discovered []store.Course) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("begin tx for course sync", "err", err)
		return
	}
	defer tx.Rollback()
	added, removed, guardSkipped, err := store.SyncCourses(ctx, tx, discovered)
	if err != nil {
		slog.Error("sync courses", "err", err)
		return
	}
	if err := tx.Commit(); err != nil {
		slog.Error("commit course sync", "err", err)
		return
	}
	if guardSkipped {
		slog.Warn("course sync deletions withheld: discovered list looked partial", "discovered", len(discovered))
	}
	if len(added) > 0 || len(removed) > 0 {
		slog.Info("course list synced", "added", len(added), "removed", len(removed))
	}
}

// scrapeActivities crawls one course at a time, resting between them. It deliberately avoids
// saia.Run's all-courses mode so the requests are spread out instead of arriving back to back.
// Returns false if the cycle was cut short.
func (s *Scheduler) scrapeActivities(ctx context.Context, sc *saia.SAIA, courses []store.Course) bool {
	for i, course := range courses {
		if i > 0 && !sleepCtx(ctx, config.ScrapeCourseRest) {
			slog.Warn("scrape cycle ended during activities", "completed_courses", i)
			return false
		}
		courseViewID := course.MoodleID
		s.underScrapeLock(func() {
			slog.Info("scraping course activities", "course", course.Name, "moodle_id", courseViewID)
			if err := sc.Run(ctx, config.SAIAPage, nil, &courseViewID); err != nil {
				slog.Error("scheduled activities scrape failed", "course", course.Name, "err", err)
			}
		})
	}
	return true
}

// scrapeGrades collects every course's grade report back to back under a single lock hold. Grade
// reports are two cheap page loads each, so they do not need the pacing the activity crawl does.
func (s *Scheduler) scrapeGrades(ctx context.Context, sc *saia.SAIA, courses []store.Course) {
	s.underScrapeLock(func() {
		for _, course := range courses {
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
