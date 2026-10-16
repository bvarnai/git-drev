package errcode_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"git-drev/internal/errcode"
)

func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{
			name:     "nil error maps to 0",
			err:      nil,
			wantCode: errcode.ExitSuccess,
		},
		{
			name:     "ErrRuntime maps to 1",
			err:      errcode.Wrap(errcode.ErrRuntime, "io failure"),
			wantCode: errcode.ExitRuntime,
		},
		{
			name:     "unknown error defaults to 1",
			err:      errors.New("unclassified error"),
			wantCode: errcode.ExitRuntime,
		},
		{
			name:     "ErrUsage maps to 2",
			err:      errcode.Wrapf(errcode.ErrUsage, "unexpected arg %q", "foo"),
			wantCode: errcode.ExitUsage,
		},
		{
			name:     "ErrConfig maps to 3",
			err:      errcode.Wrap(errcode.ErrConfig, "missing .drev.json"),
			wantCode: errcode.ExitConfig,
		},
		{
			name:     "ErrNotRepository maps to 3",
			err:      errcode.Wrap(errcode.ErrNotRepository, "not inside a working tree"),
			wantCode: errcode.ExitConfig,
		},
		{
			name:     "ErrGitState maps to 4",
			err:      errcode.Wrap(errcode.ErrGitState, "missing uuid"),
			wantCode: errcode.ExitGitState,
		},
		{
			name:     "ErrDetachedHead maps to 4",
			err:      errcode.WithHint(errcode.ErrDetachedHead, `Use "git drev select <branch>" to select a branch first.`),
			wantCode: errcode.ExitGitState,
		},
		{
			name:     "ErrGitExec maps to 5",
			err:      errcode.Join(errcode.ErrGitExec, "fetch failed", errors.New("exit status 128")),
			wantCode: errcode.ExitGitExec,
		},
		{
			name:     "ErrRemoteNotFound maps to 6",
			err:      errcode.Wrap(errcode.ErrRemoteNotFound, "remote path missing"),
			wantCode: errcode.ExitRemoteNotFound,
		},
		{
			name:     "ErrTransport maps to 7",
			err:      errcode.Wrap(errcode.ErrTransport, "ssh connection refused"),
			wantCode: errcode.ExitTransport,
		},
		{
			name:     "ErrInterrupted maps to 130",
			err:      errcode.Wrap(errcode.ErrInterrupted, "received SIGINT"),
			wantCode: errcode.ExitInterrupted,
		},
		{
			name:     "context.Canceled maps to 130",
			err:      fmt.Errorf("command aborted: %w", context.Canceled),
			wantCode: errcode.ExitInterrupted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := errcode.ExitCode(tc.err); got != tc.wantCode {
				t.Errorf("ExitCode(%v) = %d, want %d", tc.err, got, tc.wantCode)
			}
		})
	}
}

func TestHintAndJoin(t *testing.T) {
	t.Parallel()

	joined := errcode.Join(errcode.ErrDetachedHead, "Detached HEAD state detected, aborting", io.EOF)
	withHint := errcode.WithHint(joined, `Use "git drev select <branch>" to select a branch first.`)

	if !errors.Is(withHint, errcode.ErrDetachedHead) {
		t.Errorf("expected errors.Is to match ErrDetachedHead")
	}
	if !errors.Is(withHint, io.EOF) {
		t.Errorf("expected errors.Is to match joined cause io.EOF")
	}
	wantHint := `Use "git drev select <branch>" to select a branch first.`
	if got := errcode.Hint(withHint); got != wantHint {
		t.Errorf("Hint() = %q, want %q", got, wantHint)
	}
	if got := errcode.Hint(errors.New("plain")); got != "" {
		t.Errorf("Hint(plain) = %q, want empty", got)
	}
	if got := errcode.WithHint(nil, "ignored"); got != nil {
		t.Errorf("WithHint(nil) = %v, want nil", got)
	}
}
