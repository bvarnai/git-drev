package term_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bvarnai/git-drev/internal/term"
	"github.com/stretchr/testify/assert"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestSpinner_NonTTY(t *testing.T) {
	var stderr bytes.Buffer
	logger := term.New(term.Options{
		Stderr: &stderr,
		IsTTY:  false,
		Quiet:  false,
	})

	sp := logger.StartSpinner("Connecting to server...")
	sp.Update("Still connecting...")
	sp.Stop()

	out := stderr.String()
	assert.Contains(t, out, "[git-drev] Connecting to server...")
	assert.NotContains(t, out, "\r\x1b[K")
	assert.NotContains(t, out, "Still connecting...")
}

func TestSpinner_Quiet(t *testing.T) {
	var stderr bytes.Buffer
	logger := term.New(term.Options{
		Stderr: &stderr,
		IsTTY:  true,
		Quiet:  true,
	})

	sp := logger.StartSpinner("Connecting to server...")
	sp.Update("Update...")
	sp.Stop()

	assert.Empty(t, stderr.String())
}

func TestSpinner_TTY_Active(t *testing.T) {
	stderr := &safeBuffer{}
	logger := term.New(term.Options{
		Stderr:      stderr,
		IsTTY:       true,
		Quiet:       false,
		NoColorFlag: true,
	})

	sp := logger.StartSpinner("Processing task")
	time.Sleep(150 * time.Millisecond)
	sp.Update("Almost done")
	time.Sleep(150 * time.Millisecond)
	sp.Stop()

	out := stderr.String()
	assert.Contains(t, out, "[git-drev]")
	assert.Contains(t, out, "Processing task")
	assert.Contains(t, out, "Almost done")
	assert.Contains(t, out, "\r\x1b[K")
	assert.True(t, strings.HasSuffix(out, "\r\x1b[K"), "spinner must clear line on stop")
}

func TestSpinner_NilAndMultipleStop(t *testing.T) {
	var sp *term.Spinner
	assert.NotPanics(t, func() {
		sp.Update("noop")
		sp.Stop()
	})

	stderr := &safeBuffer{}
	logger := term.New(term.Options{
		Stderr: stderr,
		IsTTY:  true,
	})
	activeSp := logger.StartSpinner("Doing work")
	assert.NotPanics(t, func() {
		activeSp.Stop()
		activeSp.Stop()
	})
}
