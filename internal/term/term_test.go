package term_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"git-drev/internal/errcode"
	"git-drev/internal/term"
)

func mapEnv(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestResolveColorPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		noColorFlag bool
		isTTY       bool
		env         map[string]string
		wantColor   bool
		wantInvalid string
	}{
		{
			name:      "default auto on TTY enables color",
			isTTY:     true,
			env:       map[string]string{},
			wantColor: true,
		},
		{
			name:      "default auto on non-TTY disables color",
			isTTY:     false,
			env:       map[string]string{},
			wantColor: false,
		},
		{
			name:        "--no-color flag disables color even when GIT_DREV_COLOR=always",
			noColorFlag: true,
			isTTY:       true,
			env:         map[string]string{"GIT_DREV_COLOR": "always"},
			wantColor:   false,
		},
		{
			name:      "NO_COLOR non-empty disables color in auto mode",
			isTTY:     true,
			env:       map[string]string{"NO_COLOR": "1"},
			wantColor: false,
		},
		{
			name:      "NO_COLOR empty string does not disable color on TTY",
			isTTY:     true,
			env:       map[string]string{"NO_COLOR": ""},
			wantColor: true,
		},
		{
			name:      "TERM=dumb disables color on TTY",
			isTTY:     true,
			env:       map[string]string{"TERM": "dumb"},
			wantColor: false,
		},
		{
			name:      "GIT_DREV_COLOR=always forces color on non-TTY and overrides NO_COLOR",
			isTTY:     false,
			env:       map[string]string{"GIT_DREV_COLOR": "ALWAYS", "NO_COLOR": "1"},
			wantColor: true,
		},
		{
			name:      "GIT_DREV_COLOR=never disables color on TTY",
			isTTY:     true,
			env:       map[string]string{"GIT_DREV_COLOR": "never"},
			wantColor: false,
		},
		{
			name:        "invalid GIT_DREV_COLOR warns and falls back to auto",
			isTTY:       true,
			env:         map[string]string{"GIT_DREV_COLOR": "rainbow"},
			wantColor:   true,
			wantInvalid: "rainbow",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotColor, gotInvalid := term.ResolveColorPolicy(tc.noColorFlag, tc.isTTY, mapEnv(tc.env))
			if gotColor != tc.wantColor {
				t.Errorf("ResolveColorPolicy() useColor = %v, want %v", gotColor, tc.wantColor)
			}
			if gotInvalid != tc.wantInvalid {
				t.Errorf("ResolveColorPolicy() invalidVal = %q, want %q", gotInvalid, tc.wantInvalid)
			}
		})
	}
}

func TestLoggerOutputFormatsAndQuiet(t *testing.T) {
	t.Parallel()

	t.Run("normal and verbose output", func(t *testing.T) {
		var buf bytes.Buffer
		logger := term.New(term.Options{
			Stderr:    &buf,
			Verbose:   true,
			Quiet:     false,
			IsTTY:     false,
			EnvLookup: mapEnv(nil),
		})

		logger.Infof("Checking branch '%s'", "main")
		logger.Debug("Verbose diagnostic")
		logger.Warn("Something minor")
		logger.Hint("Use -v for details")

		err := errcode.NewError(errcode.ErrDetachedHead, "Detached HEAD state detected, aborting").
			WithHint(`Use "git drev select <branch>" to select a branch first.`)
		logger.LogError(err)

		lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
		wantLines := []string{
			"[drev] Checking branch 'main'",
			"[drev] Verbose diagnostic",
			"[drev] WARN: Something minor",
			"[drev] Hint: Use -v for details",
			"[drev] ! Detached HEAD state detected, aborting",
			`[drev] Hint: Use "git drev select <branch>" to select a branch first.`,
		}
		if len(lines) != len(wantLines) {
			t.Fatalf("got %d lines:\n%s\nwant %d lines:\n%s", len(lines), buf.String(), len(wantLines), strings.Join(wantLines, "\n"))
		}
		for i := range wantLines {
			if lines[i] != wantLines[i] {
				t.Errorf("line %d = %q, want %q", i, lines[i], wantLines[i])
			}
		}
	})

	t.Run("quiet mode suppresses Info and Debug but keeps Warn and Error", func(t *testing.T) {
		var buf bytes.Buffer
		logger := term.New(term.Options{
			Stderr:    &buf,
			Quiet:     true,
			IsTTY:     false,
			EnvLookup: mapEnv(nil),
		})

		logger.Info("suppressed info")
		logger.Debug("suppressed debug")
		logger.Hint("suppressed informational hint")
		logger.Error("fatal failure")

		got := buf.String()
		want := "[drev] ! fatal failure\n"
		if got != want {
			t.Errorf("quiet logger output = %q, want %q", got, want)
		}
	})

	t.Run("invalid GIT_DREV_COLOR emits warning on construction", func(t *testing.T) {
		var buf bytes.Buffer
		_ = term.New(term.Options{
			Stderr:    &buf,
			IsTTY:     false,
			EnvLookup: mapEnv(map[string]string{"GIT_DREV_COLOR": "bogus"}),
		})
		if !strings.Contains(buf.String(), `[drev] WARN: Invalid GIT_DREV_COLOR value "bogus"`) {
			t.Errorf("expected warning in stderr, got %q", buf.String())
		}
	})
}

func TestTimer(t *testing.T) {
	t.Parallel()

	current := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	timer := term.StartTimerWithClock(func() time.Time {
		return current
	})

	current = current.Add(125 * time.Second)
	if got := timer.Elapsed(); got != 125*time.Second {
		t.Errorf("timer.Elapsed() = %v, want 125s", got)
	}
	if got := timer.String(); got != "Took 2 min 5 sec" {
		t.Errorf("timer.String() = %q, want %q", got, "Took 2 min 5 sec")
	}
	if got := term.FormatDuration(-5 * time.Second); got != "Took 0 min 0 sec" {
		t.Errorf("FormatDuration(-5s) = %q, want %q", got, "Took 0 min 0 sec")
	}
}
