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
			err:      errcode.NewError(errcode.ErrRuntime, "io failure"),
			wantCode: errcode.ExitRuntime,
		},
		{
			name:     "unknown error defaults to 1",
			err:      errors.New("unclassified error"),
			wantCode: errcode.ExitRuntime,
		},
		{
			name:     "ErrUsage maps to 2",
			err:      errcode.NewError(errcode.ErrUsage, "unexpected arg %q", "foo"),
			wantCode: errcode.ExitUsage,
		},
		{
			name:     "ErrConfig maps to 3",
			err:      errcode.NewError(errcode.ErrConfig, "missing .drev.json"),
			wantCode: errcode.ExitConfig,
		},
		{
			name:     "ErrNotRepository maps to 3",
			err:      errcode.NewError(errcode.ErrNotRepository, "not inside a working tree"),
			wantCode: errcode.ExitConfig,
		},
		{
			name:     "ErrGitState maps to 4",
			err:      errcode.NewError(errcode.ErrGitState, "missing uuid"),
			wantCode: errcode.ExitGitState,
		},
		{
			name:     "ErrDetachedHead maps to 4",
			err:      errcode.NewError(errcode.ErrDetachedHead, "Detached HEAD").WithHint(`Use "git drev select <branch>" to select a branch first.`),
			wantCode: errcode.ExitGitState,
		},
		{
			name:     "ErrGitExec maps to 5",
			err:      errcode.WrapError(errcode.ErrGitExec, errors.New("exit status 128"), "fetch failed"),
			wantCode: errcode.ExitGitExec,
		},
		{
			name:     "ErrRemoteNotFound maps to 6",
			err:      errcode.NewError(errcode.ErrRemoteNotFound, "remote path missing"),
			wantCode: errcode.ExitRemoteNotFound,
		},
		{
			name:     "ErrTransport maps to 7",
			err:      errcode.NewError(errcode.ErrTransport, "ssh connection refused"),
			wantCode: errcode.ExitTransport,
		},
		{
			name:     "ErrInterrupted maps to 130",
			err:      errcode.NewError(errcode.ErrInterrupted, "received SIGINT"),
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

func TestAppErrorFormattingAndHint(t *testing.T) {
	t.Parallel()

	err := errcode.NewError(errcode.ErrUsage, "Invalid branch name '%s'", "bad..branch")
	if err.Error() != "Invalid branch name 'bad..branch'" {
		t.Errorf("err.Error() = %q, want clean message without category prefix", err.Error())
	}
	if !errors.Is(err, errcode.ErrUsage) {
		t.Errorf("expected errors.Is to match ErrUsage")
	}

	wrapped := errcode.WrapError(errcode.ErrDetachedHead, io.EOF, "Detached HEAD state detected, aborting").
		WithHint(`Use "git drev select <branch>" to select a branch first.`)

	if !errors.Is(wrapped, errcode.ErrDetachedHead) {
		t.Errorf("expected errors.Is to match ErrDetachedHead")
	}
	if !errors.Is(wrapped, io.EOF) {
		t.Errorf("expected errors.Is to match wrapped cause io.EOF")
	}
	wantHint := `Use "git drev select <branch>" to select a branch first.`
	if got := errcode.Hint(wrapped); got != wantHint {
		t.Errorf("Hint() = %q, want %q", got, wantHint)
	}
	if got := errcode.Hint(errors.New("plain")); got != "" {
		t.Errorf("Hint(plain) = %q, want empty", got)
	}
}
