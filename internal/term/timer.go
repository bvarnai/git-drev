package term

import (
	"fmt"
	"time"
)

// Timer measures elapsed wall-clock duration for a single operation independently
// of Logger state.
type Timer struct {
	start time.Time
	now   func() time.Time
}

// StartTimer starts a new Timer using the system clock (time.Now).
func StartTimer() Timer {
	return StartTimerWithClock(time.Now)
}

// StartTimerWithClock starts a new Timer using a custom clock function (for testing).
func StartTimerWithClock(now func() time.Time) Timer {
	if now == nil {
		now = time.Now
	}
	return Timer{
		start: now(),
		now:   now,
	}
}

// Elapsed returns the duration elapsed since the Timer was started.
func (t Timer) Elapsed() time.Duration {
	if t.now == nil {
		return 0
	}
	d := t.now().Sub(t.start)
	if d < 0 {
		return 0
	}
	return d
}

// String returns the canonical elapsed summary string ("Took X min Y sec").
func (t Timer) String() string {
	return FormatDuration(t.Elapsed())
}

// FormatDuration formats d as "Took X min Y sec".
func FormatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSeconds := int64(d.Seconds())
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("Took %d min %d sec", minutes, seconds)
}
