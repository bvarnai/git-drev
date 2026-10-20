package storage_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/bvarnai/git-drev/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockExitError struct {
	code int
	msg  string
}

func (m *mockExitError) Error() string {
	return m.msg
}

func (m *mockExitError) ExitCode() int {
	return m.code
}

func TestCheckDirExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("directory exists", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunFn: func(ctx context.Context, host, cmd string) (string, string, error) {
				return "", "", nil
			},
		}
		client := storage.NewClient(ssh, &transport.MockRsyncRunner{}, nil, nil)

		exists, err := client.CheckDirExists(ctx, "host1", "/path/to/dir")
		require.NoError(t, err)
		assert.True(t, exists)
		require.Len(t, ssh.Calls, 1)
		assert.Equal(t, "Run host1 test -d '/path/to/dir'", ssh.Calls[0])
	})

	t.Run("directory does not exist (exit code 1)", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunFn: func(ctx context.Context, host, cmd string) (string, string, error) {
				return "", "", &mockExitError{code: 1, msg: "exit status 1"}
			},
		}
		client := storage.NewClient(ssh, &transport.MockRsyncRunner{}, nil, nil)

		exists, err := client.CheckDirExists(ctx, "host1", "/path/to/dir")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("ssh execution failure", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunFn: func(ctx context.Context, host, cmd string) (string, string, error) {
				return "", "Permission denied", errors.New("exit status 255")
			},
		}
		client := storage.NewClient(ssh, &transport.MockRsyncRunner{}, nil, nil)

		exists, err := client.CheckDirExists(ctx, "host1", "/path/to/dir")
		require.Error(t, err)
		assert.False(t, exists)
		assert.Equal(t, errcode.ExitTransport, errcode.ExitCode(err))
	})
}

func TestCloneBaseline(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("successful clone", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunFn: func(ctx context.Context, host, cmd string) (string, string, error) {
				return "", "", nil
			},
		}
		client := storage.NewClient(ssh, &transport.MockRsyncRunner{}, nil, nil)

		err := client.CloneBaseline(ctx, "host1", "/src", "/dst")
		require.NoError(t, err)
		require.Len(t, ssh.Calls, 1)
		assert.Equal(t, "Run host1 cp -al -- '/src' '/dst'", ssh.Calls[0])
	})

	t.Run("clone failure", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunFn: func(ctx context.Context, host, cmd string) (string, string, error) {
				return "", "cp: cannot create link", errors.New("exit status 1")
			},
		}
		client := storage.NewClient(ssh, &transport.MockRsyncRunner{}, nil, nil)

		err := client.CloneBaseline(ctx, "host1", "/src", "/dst")
		require.Error(t, err)
		assert.Equal(t, errcode.ExitTransport, errcode.ExitCode(err))
	})
}

func TestRemoveDir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("successful remove", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunFn: func(ctx context.Context, host, cmd string) (string, string, error) {
				return "", "", nil
			},
		}
		client := storage.NewClient(ssh, &transport.MockRsyncRunner{}, nil, nil)

		err := client.RemoveDir(ctx, "host1", "/path")
		require.NoError(t, err)
		require.Len(t, ssh.Calls, 1)
		assert.Equal(t, "Run host1 rm -rf -- '/path'", ssh.Calls[0])
	})

	t.Run("remove failure", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunFn: func(ctx context.Context, host, cmd string) (string, string, error) {
				return "", "rm: permission denied", errors.New("exit status 1")
			},
		}
		client := storage.NewClient(ssh, &transport.MockRsyncRunner{}, nil, nil)

		err := client.RemoveDir(ctx, "host1", "/path")
		require.Error(t, err)
		assert.Equal(t, errcode.ExitTransport, errcode.ExitCode(err))
	})
}

func TestQueryUsage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("successful fast query", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunStreamFn: func(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) (string, error) {
				output := "COUNT:1\nTOTAL_SIZE:1.5G\nREV:550e8400-e29b-41d4-a716-446655440000:-:0\n"
				pipeErr := stdoutPipe(strings.NewReader(output))
				return "", pipeErr
			},
		}
		client := storage.NewClient(ssh, &transport.MockRsyncRunner{}, nil, nil)

		var progressCalled bool
		total, revs, err := client.QueryUsage(ctx, "host1", "/storage", storage.QueryUsageOptions{
			Verbose: false,
			OnProgress: func(curr, tot int, uuid string) {
				progressCalled = true
			},
		})
		require.NoError(t, err)
		assert.True(t, progressCalled)
		assert.Equal(t, "1.5G", total)
		require.Len(t, revs, 1)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", revs[0].UUID)
	})

	t.Run("remote path not found", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunStreamFn: func(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) (string, error) {
				return "ERROR:Remote path '/storage' does not exist.", &mockExitError{code: 66, msg: "exit status 66"}
			},
		}
		client := storage.NewClient(ssh, &transport.MockRsyncRunner{}, nil, nil)

		_, _, err := client.QueryUsage(ctx, "host1", "/storage")
		require.Error(t, err)
		assert.Equal(t, errcode.ExitRemoteNotFound, errcode.ExitCode(err))
	})
}

func TestDownloadAndUpload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("Download delegates to RsyncRunner with IsUpload=false", func(t *testing.T) {
		var capturedOpts transport.RsyncOptions
		rsync := &transport.MockRsyncRunner{
			SyncFn: func(ctx context.Context, opts transport.RsyncOptions) error {
				capturedOpts = opts
				return nil
			},
		}
		client := storage.NewClient(&transport.MockSSHRunner{}, rsync, nil, nil)

		err := client.Download(ctx, storage.TransferRequest{
			Host:       "host1",
			RemotePath: "/remote",
			LocalPath:  "/local",
		})
		require.NoError(t, err)
		assert.False(t, capturedOpts.IsUpload)
		assert.Equal(t, "host1", capturedOpts.Host)
		assert.Equal(t, "/remote", capturedOpts.RemotePath)
		assert.Equal(t, "/local", capturedOpts.LocalPath)
	})

	t.Run("Upload delegates to RsyncRunner with IsUpload=true", func(t *testing.T) {
		var capturedOpts transport.RsyncOptions
		rsync := &transport.MockRsyncRunner{
			SyncFn: func(ctx context.Context, opts transport.RsyncOptions) error {
				capturedOpts = opts
				return nil
			},
		}
		client := storage.NewClient(&transport.MockSSHRunner{}, rsync, nil, nil)

		err := client.Upload(ctx, storage.TransferRequest{
			Host:       "host1",
			RemotePath: "/remote",
			LocalPath:  "/local",
		})
		require.NoError(t, err)
		assert.True(t, capturedOpts.IsUpload)
		assert.Equal(t, "host1", capturedOpts.Host)
		assert.Equal(t, "/remote", capturedOpts.RemotePath)
		assert.Equal(t, "/local", capturedOpts.LocalPath)
	})
}

func TestParseQueryUsageOutput(t *testing.T) {
	t.Parallel()

	output := `
TOTAL_SIZE:2.4G
REV:550E8400-E29B-41D4-A716-446655440000:1.2G:1700000000
REV:c1ca82b1-7f34-4f4c-9a76-05e3297b2a23:800M:1700001000
`

	totalSize, revs := storage.ParseQueryUsageOutput(output)
	assert.Equal(t, "2.4G", totalSize)
	require.Len(t, revs, 2)

	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", revs[0].UUID)
	assert.Equal(t, "1.2G", revs[0].SizeHuman)
	assert.Equal(t, time.Unix(1700000000, 0), revs[0].ModTime)

	assert.Equal(t, "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23", revs[1].UUID)
	assert.Equal(t, "800M", revs[1].SizeHuman)
	assert.Equal(t, time.Unix(1700001000, 0), revs[1].ModTime)

	// Test streaming indexed format
	streamingOutput := `
TOTAL_SIZE:5.0G
COUNT:2
REV:550e8400-e29b-41d4-a716-446655440000:2.5G:1700000000:1:2
REV:c1ca82b1-7f34-4f4c-9a76-05e3297b2a23:2.5G:1700001000:2:2
`
	totalSizeStream, revsStream := storage.ParseQueryUsageOutput(streamingOutput)
	assert.Equal(t, "5.0G", totalSizeStream)
	require.Len(t, revsStream, 2)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", revsStream[0].UUID)
	assert.Equal(t, "2.5G", revsStream[0].SizeHuman)
	assert.Equal(t, time.Unix(1700000000, 0), revsStream[0].ModTime)

	// Test fast summary mode output
	summaryOutput := `
TOTAL_SIZE:1.8G
COUNT:1
REV:550e8400-e29b-41d4-a716-446655440000:-:0
`
	totalSizeSummary, revsSummary := storage.ParseQueryUsageOutput(summaryOutput)
	assert.Equal(t, "1.8G", totalSizeSummary)
	require.Len(t, revsSummary, 1)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", revsSummary[0].UUID)
	assert.Equal(t, "-", revsSummary[0].SizeHuman)
	assert.True(t, revsSummary[0].ModTime.IsZero())
}
