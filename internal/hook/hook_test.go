package hook_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/hook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		val         string
		set         bool
		want        time.Duration
		expectError bool
	}{
		{
			name: "unset returns default 5m",
			set:  false,
			want: 5 * time.Minute,
		},
		{
			name: "empty returns default 5m",
			set:  true,
			val:  "",
			want: 5 * time.Minute,
		},
		{
			name: "valid duration 10s",
			set:  true,
			val:  "10s",
			want: 10 * time.Second,
		},
		{
			name:        "invalid format returns ErrUsage",
			set:         true,
			val:         "not-a-duration",
			expectError: true,
		},
		{
			name:        "negative duration returns ErrUsage",
			set:         true,
			val:         "-5s",
			expectError: true,
		},
		{
			name:        "zero duration returns ErrUsage",
			set:         true,
			val:         "0s",
			expectError: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d, err := hook.ResolveTimeout(func(key string) (string, bool) {
				if key == hook.EnvTimeout {
					return tt.val, tt.set
				}
				return "", false
			})
			if tt.expectError {
				require.Error(t, err)
				assert.Equal(t, errcode.ExitUsage, errcode.ExitCode(err))
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, d)
			}
		})
	}
}

func TestExecRunner_StatPreconditions(t *testing.T) {
	t.Parallel()

	runner := hook.NewExecRunner()
	tempDir := t.TempDir()

	// 1. Missing hook file
	missingPath := filepath.Join(tempDir, "missing-hook")
	err := runner.Run(context.Background(), hook.RunRequest{
		Kind: hook.KindInfo,
		Path: missingPath,
	})
	assert.ErrorIs(t, err, fs.ErrNotExist)

	// 2. Directory instead of file
	dirPath := filepath.Join(tempDir, "dir-hook")
	require.NoError(t, os.Mkdir(dirPath, 0o755))
	err = runner.Run(context.Background(), hook.RunRequest{
		Kind: hook.KindInfo,
		Path: dirPath,
	})
	require.Error(t, err)
	assert.Equal(t, errcode.ExitRuntime, errcode.ExitCode(err))
	assert.Contains(t, err.Error(), "is a directory, expected file")

	// 3. Broken symlink
	brokenSymlink := filepath.Join(tempDir, "broken-symlink")
	if err := os.Symlink(filepath.Join(tempDir, "nonexistent-target"), brokenSymlink); err == nil {
		err = runner.Run(context.Background(), hook.RunRequest{
			Kind: hook.KindInfo,
			Path: brokenSymlink,
		})
		require.Error(t, err)
		assert.Equal(t, errcode.ExitRuntime, errcode.ExitCode(err))
		assert.Contains(t, err.Error(), "is a broken symlink")
	}

	// 4. Empty hook file (0 bytes) is a valid no-op
	emptyPath := filepath.Join(tempDir, "empty-hook")
	require.NoError(t, os.WriteFile(emptyPath, []byte{}, 0o644))
	err = runner.Run(context.Background(), hook.RunRequest{
		Kind: hook.KindInfo,
		Path: emptyPath,
	})
	assert.NoError(t, err)
}

func TestExecRunner_ExecutionModesAndEnv(t *testing.T) {
	t.Parallel()

	runner := hook.NewExecRunner()
	tempDir := t.TempDir()

	env := hook.Env{
		Version: "1.2.3",
		UUID:    "550e8400-e29b-41d4-a716-446655440000",
		Seq:     "42",
		Branch:  "feature/hooks",
		WorkDir: filepath.Join(tempDir, "data"),
		Root:    tempDir,
	}

	t.Run("info_hook function execution with env vars", func(t *testing.T) {
		hookFile := filepath.Join(tempDir, ".git-drev-info-hook")
		content := `
info_hook() {
  echo "version=${GIT_DREV_VERSION} uuid=${GIT_DREV_UUID} seq=${GIT_DREV_SEQ} branch=${GIT_DREV_BRANCH} type=${GIT_DREV_HOOK_TYPE}"
}
`
		require.NoError(t, os.WriteFile(hookFile, []byte(content), 0o644))

		var stdout, stderr bytes.Buffer
		err := runner.Run(context.Background(), hook.RunRequest{
			Kind:    hook.KindInfo,
			Path:    hookFile,
			WorkDir: tempDir,
			Env:     env,
			Stdout:  &stdout,
			Stderr:  &stderr,
		})
		require.NoError(t, err)
		assert.Equal(t, "version=1.2.3 uuid=550e8400-e29b-41d4-a716-446655440000 seq=42 branch=feature/hooks type=info\n", stdout.String())
	})

	t.Run("put_hook function with extra arguments", func(t *testing.T) {
		hookFile := filepath.Join(tempDir, ".git-drev-put-hook")
		content := `
put_hook() {
  echo "args: $1, $2; uuid=${GIT_DREV_UUID}"
}
`
		require.NoError(t, os.WriteFile(hookFile, []byte(content), 0o644))

		var stdout, stderr bytes.Buffer
		err := runner.Run(context.Background(), hook.RunRequest{
			Kind:    hook.KindPut,
			Path:    hookFile,
			WorkDir: tempDir,
			Args:    []string{"first-arg", "second-arg"},
			Env:     env,
			Stdout:  &stdout,
			Stderr:  &stderr,
		})
		require.NoError(t, err)
		assert.Equal(t, "args: first-arg, second-arg; uuid=550e8400-e29b-41d4-a716-446655440000\n", stdout.String())
	})

	t.Run("shell script without explicit hook function executes top-level", func(t *testing.T) {
		hookFile := filepath.Join(tempDir, "top-level-hook.sh")
		content := `
echo "top-level: branch=${GIT_DREV_BRANCH}"
`
		require.NoError(t, os.WriteFile(hookFile, []byte(content), 0o644))

		var stdout, stderr bytes.Buffer
		err := runner.Run(context.Background(), hook.RunRequest{
			Kind:    hook.KindInfo,
			Path:    hookFile,
			WorkDir: tempDir,
			Env:     env,
			Stdout:  &stdout,
			Stderr:  &stderr,
		})
		require.NoError(t, err)
		assert.Equal(t, "top-level: branch=feature/hooks\n", stdout.String())
	})

	t.Run("failing hook returns ErrRuntime with exit code", func(t *testing.T) {
		hookFile := filepath.Join(tempDir, "failing-hook.sh")
		content := `
info_hook() {
  echo "critical failure" >&2
  exit 42
}
`
		require.NoError(t, os.WriteFile(hookFile, []byte(content), 0o644))

		var stdout, stderr bytes.Buffer
		err := runner.Run(context.Background(), hook.RunRequest{
			Kind:    hook.KindInfo,
			Path:    hookFile,
			WorkDir: tempDir,
			Env:     env,
			Stdout:  &stdout,
			Stderr:  &stderr,
		})
		require.Error(t, err)
		assert.Equal(t, errcode.ExitRuntime, errcode.ExitCode(err))
		assert.Contains(t, err.Error(), "failed with exit code 42")
		assert.Contains(t, stderr.String(), "critical failure")
	})

	t.Run("hook exceeding timeout is terminated and returns ErrRuntime", func(t *testing.T) {
		hookFile := filepath.Join(tempDir, "timeout-hook.sh")
		content := `
info_hook() {
  sleep 5
}
`
		require.NoError(t, os.WriteFile(hookFile, []byte(content), 0o644))

		err := runner.Run(context.Background(), hook.RunRequest{
			Kind:    hook.KindInfo,
			Path:    hookFile,
			WorkDir: tempDir,
			Timeout: 100 * time.Millisecond,
		})
		require.Error(t, err)
		assert.Equal(t, errcode.ExitRuntime, errcode.ExitCode(err))
		assert.Contains(t, err.Error(), "timed out after 100ms")
	})

	t.Run("interrupted context returns ErrInterrupted", func(t *testing.T) {
		hookFile := filepath.Join(tempDir, "interrupt-hook.sh")
		content := `
info_hook() {
  sleep 5
}
`
		require.NoError(t, os.WriteFile(hookFile, []byte(content), 0o644))

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		err := runner.Run(ctx, hook.RunRequest{
			Kind:    hook.KindInfo,
			Path:    hookFile,
			WorkDir: tempDir,
			Timeout: 10 * time.Second,
		})
		require.Error(t, err)
		assert.True(t, errors.Is(err, errcode.ErrInterrupted) || errors.Is(err, context.Canceled))
		assert.Equal(t, errcode.ExitInterrupted, errcode.ExitCode(err))
	})
}
