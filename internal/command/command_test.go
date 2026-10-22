package command_test

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/bvarnai/git-drev/internal/command"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := command.New(ctx, "echo", "hello", "world")
	require.NotNil(t, cmd)
	assert.Equal(t, []string{"echo", "hello", "world"}, cmd.Args)
	assert.Equal(t, 3*time.Second, cmd.WaitDelay)
	assert.NotNil(t, cmd.Cancel)

	// Verify env sanitization
	envMap := make(map[string]string)
	for _, entry := range cmd.Env {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			envMap[k] = v
		}
	}
	assert.Equal(t, "C", envMap["LC_ALL"])
	assert.Equal(t, "C", envMap["LANG"])
	assert.Equal(t, "0", envMap["GIT_TERMINAL_PROMPT"])
	assert.Equal(t, "0", envMap["GIT_ADVICE"])
}

func TestSanitizedEnv(t *testing.T) {
	t.Parallel()

	base := []string{
		"PATH=/usr/bin",
		"LC_ALL=hu_HU.UTF-8",
		"LANG=de_DE.UTF-8",
		"GIT_TERMINAL_PROMPT=1",
		"GIT_ADVICE=1",
		"MY_CUSTOM_VAR=value123",
		"OVERRIDDEN_VAR=old",
	}

	extra := []string{"EXTRA_VAR=foo", "OVERRIDDEN_VAR=new", "LANG=fr_FR.UTF-8"}

	env := command.SanitizedEnv(base, extra)

	got := make(map[string]string, len(env))
	counts := make(map[string]int, len(env))
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			got[k] = v
			counts[k]++
		}
	}

	assert.Equal(t, "C", got["LC_ALL"])
	assert.Equal(t, "C", got["LANG"])
	assert.Equal(t, "0", got["GIT_TERMINAL_PROMPT"])
	assert.Equal(t, "0", got["GIT_ADVICE"])
	assert.Equal(t, "value123", got["MY_CUSTOM_VAR"])
	assert.Equal(t, "foo", got["EXTRA_VAR"])
	assert.Equal(t, "new", got["OVERRIDDEN_VAR"])
	assert.Equal(t, 1, counts["OVERRIDDEN_VAR"], "expected OVERRIDDEN_VAR to be deduplicated")
	assert.Equal(t, 1, counts["LANG"], "expected LANG to appear once")
}

func TestError_FormatAndUnwrap(t *testing.T) {
	t.Parallel()

	t.Run("default category and combined output with stderr and stdout", func(t *testing.T) {
		err := &command.Error{
			Command:  "git",
			Args:     []string{"status", "--short"},
			ExitCode: 1,
			Stdout:   "fatal output line 1",
			Stderr:   "error detail line 2",
		}

		assert.Contains(t, err.Error(), "git status --short failed (exit 1)")
		assert.Contains(t, err.Error(), "fatal output line 1\nerror detail line 2")
		assert.True(t, errors.Is(err, errcode.ErrTransport))

		code, ok := command.ExitCodeOf(err)
		assert.True(t, ok)
		assert.Equal(t, 1, code)
	})

	t.Run("custom category errcode.ErrGitExec", func(t *testing.T) {
		err := &command.Error{
			Command:  "git",
			Args:     []string{"fetch"},
			ExitCode: 128,
			Stderr:   "fatal: repository not found",
			Category: errcode.ErrGitExec,
		}

		assert.Equal(t, "git fetch failed (exit 128): fatal: repository not found", err.Error())
		assert.True(t, errors.Is(err, errcode.ErrGitExec))
		assert.False(t, errors.Is(err, errcode.ErrTransport))

		code, ok := command.ExitCodeOf(err)
		assert.True(t, ok)
		assert.Equal(t, 128, code)
	})

	t.Run("NewError constructor", func(t *testing.T) {
		cmd := exec.Command("rsync", "-a", "/src", "/dst")
		cmdErr := command.NewError(cmd, nil, "some stdout", "some stderr", nil)

		assert.Equal(t, "rsync", cmdErr.Command)
		assert.Equal(t, []string{"-a", "/src", "/dst"}, cmdErr.Args)
		assert.Equal(t, -1, cmdErr.ExitCode)
		assert.Equal(t, "some stdout", cmdErr.Stdout)
		assert.Equal(t, "some stderr", cmdErr.Stderr)
		assert.Equal(t, errcode.ErrTransport, cmdErr.Category)
	})

	t.Run("ExitCodeOf non-command error", func(t *testing.T) {
		stdErr := errors.New("generic error")
		code, ok := command.ExitCodeOf(stdErr)
		assert.False(t, ok)
		assert.Equal(t, -1, code)
	})

	t.Run("StderrOrError", func(t *testing.T) {
		assert.Equal(t, "", command.StderrOrError(nil))
		assert.Equal(t, "plain error", command.StderrOrError(errors.New("plain error")))
		assert.Equal(t, "trimmed stderr", command.StderrOrError(&command.Error{Command: "ssh", ExitCode: 1, Stderr: "  trimmed stderr \n"}))
		assert.Equal(t, "ssh failed (exit 1)", command.StderrOrError(&command.Error{Command: "ssh", ExitCode: 1}))
	})
}

func TestRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("successful execution returns trimmed-capable stdout", func(t *testing.T) {
		out, err := command.Run(ctx, command.Request{
			Name: "echo",
			Args: []string{"hello", "world"},
		})
		require.NoError(t, err)
		assert.Equal(t, "hello world\n", out)
	})

	t.Run("environment and working directory propagation", func(t *testing.T) {
		tempDir := t.TempDir()
		out, err := command.Run(ctx, command.Request{
			Name:     "sh",
			Args:     []string{"-c", "pwd && echo $TEST_RUN_VAR"},
			Dir:      tempDir,
			ExtraEnv: []string{"TEST_RUN_VAR=drev_refactored"},
		})
		require.NoError(t, err)
		assert.Contains(t, out, tempDir)
		assert.Contains(t, out, "drev_refactored")
	})

	t.Run("streaming stdout and stderr with teeing", func(t *testing.T) {
		var streamOut, streamErr strings.Builder
		out, err := command.Run(ctx, command.Request{
			Name:   "sh",
			Args:   []string{"-c", "echo stdout_data; echo stderr_data >&2"},
			Stdout: &streamOut,
			Stderr: &streamErr,
		})
		require.NoError(t, err)
		assert.Equal(t, "stdout_data\n", streamOut.String())
		assert.Equal(t, "stderr_data\n", streamErr.String())
		assert.Equal(t, "stdout_data\n", out)
	})

	t.Run("non-zero exit maps to command.Error with category and stderr", func(t *testing.T) {
		out, err := command.Run(ctx, command.Request{
			Name:     "sh",
			Args:     []string{"-c", "echo standard_out; echo specific_failure >&2; exit 42"},
			Category: errcode.ErrGitExec,
		})
		require.Error(t, err)
		assert.Equal(t, "standard_out\n", out)

		var cmdErr *command.Error
		require.True(t, errors.As(err, &cmdErr))
		assert.Equal(t, 42, cmdErr.ExitCode)
		assert.Equal(t, "specific_failure", cmdErr.Stderr)
		assert.Equal(t, "standard_out\n", cmdErr.Stdout)
		assert.True(t, errors.Is(err, errcode.ErrGitExec))
	})

	t.Run("pre-canceled context returns ErrInterrupted", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := command.Run(canceledCtx, command.Request{
			Name: "echo",
			Args: []string{"unreachable"},
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, errcode.ErrInterrupted))
	})

	t.Run("stdout pipe streaming and callback", func(t *testing.T) {
		var collected strings.Builder
		_, err := command.Run(ctx, command.Request{
			Name: "sh",
			Args: []string{"-c", "echo piped_output"},
			StdoutPipe: func(r io.Reader) error {
				buf := make([]byte, 128)
				n, readErr := r.Read(buf)
				if readErr != nil && !errors.Is(readErr, io.EOF) {
					return readErr
				}
				collected.Write(buf[:n])
				return nil
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "piped_output\n", collected.String())
	})

	t.Run("stdout pipe callback error propagation", func(t *testing.T) {
		expectedPipeErr := errors.New("custom pipe error")
		_, err := command.Run(ctx, command.Request{
			Name: "sh",
			Args: []string{"-c", "echo piped_output"},
			StdoutPipe: func(r io.Reader) error {
				return expectedPipeErr
			},
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, expectedPipeErr))
	})
}
