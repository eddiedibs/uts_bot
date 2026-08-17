package scheduler

import (
	"context"
	"testing"
	"time"

	"uts_bot/internal/config"
)

func TestCycleDeadlineCoversEveryRest(t *testing.T) {
	original := [2]time.Duration{config.ScrapeCourseRest, config.ScrapePhaseRest}
	t.Cleanup(func() {
		config.ScrapeCourseRest, config.ScrapePhaseRest = original[0], original[1]
	})

	config.ScrapeCourseRest = 2 * time.Minute
	config.ScrapePhaseRest = 5 * time.Minute

	const n = 7
	restsAlone := (n-1)*config.ScrapeCourseRest + config.ScrapePhaseRest

	got := cycleDeadline(n)
	if got <= restsAlone {
		t.Fatalf("cycleDeadline(%d) = %v, which leaves no time for actual work beyond %v of rests", n, got, restsAlone)
	}

	// Raising a rest must push the deadline out, or long rests would truncate cycles silently.
	config.ScrapeCourseRest = 10 * time.Minute
	if raised := cycleDeadline(n); raised <= got {
		t.Fatalf("cycleDeadline(%d) = %v after raising the per-course rest, want more than %v", n, raised, got)
	}
}

func TestSleepCtxReturnsEarlyOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	if sleepCtx(ctx, 10*time.Second) {
		t.Fatal("sleepCtx reported a completed sleep, want false after cancellation")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("sleepCtx blocked for %v; shutdown would stall behind a rest", elapsed)
	}
}

func TestSleepCtxCompletes(t *testing.T) {
	t.Parallel()

	if !sleepCtx(context.Background(), time.Millisecond) {
		t.Fatal("sleepCtx(1ms) = false, want true")
	}
	if !sleepCtx(context.Background(), 0) {
		t.Fatal("sleepCtx(0) = false, want true for a no-op rest")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if sleepCtx(cancelled, 0) {
		t.Fatal("sleepCtx(0) on a cancelled context = true, want false")
	}
}

func TestWithinActiveHours(t *testing.T) {
	original := [2]int{config.ScrapeActiveStartHour, config.ScrapeActiveEndHour}
	t.Cleanup(func() {
		config.ScrapeActiveStartHour, config.ScrapeActiveEndHour = original[0], original[1]
	})
	config.ScrapeActiveStartHour, config.ScrapeActiveEndHour = 7, 23

	at := func(hour int) time.Time {
		return time.Date(2026, 8, 9, hour, 30, 0, 0, time.Local)
	}
	tests := []struct {
		hour int
		want bool
	}{
		{6, false},
		{7, true},  // start is inclusive
		{22, true}, // end is exclusive
		{23, false},
		{0, false},
	}
	for _, tt := range tests {
		if got := withinActiveHours(at(tt.hour)); got != tt.want {
			t.Errorf("withinActiveHours(%02d:30) = %v, want %v", tt.hour, got, tt.want)
		}
	}
}
