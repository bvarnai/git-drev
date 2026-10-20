package term

import (
	"fmt"
	"sync"
	"time"
)

var defaultFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner provides an animated busy-wait indicator on stderr for long-running operations.
type Spinner struct {
	logger    *Logger
	mu        sync.Mutex
	msg       string
	startTime time.Time
	stopCh    chan struct{}
	doneCh    chan struct{}
	active    bool
}

// StartSpinner initiates an animated spinner on stderr if stderr is an interactive TTY and quiet is false.
// If stderr is not a TTY (CI logs, redirects), it logs initialMsg once via logger.Info and returns an inactive Spinner.
// If quiet mode is active or logger is nil, it returns a silent inactive Spinner.
func (l *Logger) StartSpinner(initialMsg string) *Spinner {
	if l == nil {
		return &Spinner{}
	}
	if l.quiet {
		return &Spinner{logger: l}
	}
	if !l.isTTY {
		l.Info(initialMsg)
		return &Spinner{logger: l}
	}

	s := &Spinner{
		logger:    l,
		msg:       initialMsg,
		startTime: time.Now(),
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		active:    true,
	}

	go s.run()
	return s
}

func (s *Spinner) run() {
	defer close(s.doneCh)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	frameIdx := 0
	for {
		s.mu.Lock()
		msg := s.msg
		elapsed := int(time.Since(s.startTime).Seconds())
		frame := defaultFrames[frameIdx%len(defaultFrames)]
		frameIdx++
		s.mu.Unlock()

		s.logger.mu.Lock()
		if s.logger.useColor {
			fmt.Fprintf(s.logger.stderr, "\r\x1b[K%s %s%s%s %s (%ds)", Prefix, ansiCyan, frame, ansiReset, msg, elapsed)
		} else {
			fmt.Fprintf(s.logger.stderr, "\r\x1b[K%s %s %s (%ds)", Prefix, frame, msg, elapsed)
		}
		s.logger.mu.Unlock()

		select {
		case <-s.stopCh:
			s.logger.mu.Lock()
			fmt.Fprint(s.logger.stderr, "\r\x1b[K")
			s.logger.mu.Unlock()
			return
		case <-ticker.C:
		}
	}
}

// Update dynamically changes the status message displayed alongside the spinner.
func (s *Spinner) Update(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return
	}
	s.msg = msg
}

// Stop cleanly clears the spinner from the terminal and terminates the animation goroutine.
// Safe to call multiple times or on nil/inactive spinners.
func (s *Spinner) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	close(s.stopCh)
	s.mu.Unlock()

	<-s.doneCh
}
