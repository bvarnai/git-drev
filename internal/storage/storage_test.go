package storage_test

import (
	"context"
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

func TestCheckDirExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("directory exists", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunFn: func(ctx context.Context, host, cmd string) (string, error) {
				return "", nil
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
			RunFn: func(ctx context.Context, host, cmd string) (string, error) {
				return "", &transport.CommandError{
					Command:  "ssh",
					Args:     []string{"-T", host, cmd},
					ExitCode: 1,
					Stderr:   "directory not found",
				}
			},
		}
		client := storage.NewClient(ssh, &transport.MockRsyncRunner{}, nil, nil)

		exists, err := client.CheckDirExists(ctx, "host1", "/path/to/dir")
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("ssh execution failure", func(t *testing.T) {
		ssh := &transport.MockSSHRunner{
			RunFn: func(ctx context.Context, host, cmd string) (string, error) {
				return "", &transport.CommandError{
					Command:  "ssh",
					Args:     []string{"-T", host, cmd},
					ExitCode: 255,
					Stderr:   "Permission denied",
				}
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
			RunFn: func(ctx context.Context, host, cmd string) (string, error) {
				return "", nil
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
			RunFn: func(ctx context.Context, host, cmd string) (string, error) {
				return "", &transport.CommandError{
					Command:  "ssh",
					Args:     []string{"-T", host, cmd},
					ExitCode: 1,
					Stderr:   "cp: cannot create link",
				}
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
			RunFn: func(ctx context.Context, host, cmd string) (string, error) {
				return "", nil
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
			RunFn: func(ctx context.Context, host, cmd string) (string, error) {
				return "", &transport.CommandError{
					Command:  "ssh",
					Args:     []string{"-T", host, cmd},
					ExitCode: 1,
					Stderr:   "rm: permission denied",
				}
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
			RunStreamFn: func(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) error {
				output := "COUNT:1\nTOTAL_SIZE:1.5G\nREV:550e8400-e29b-41d4-a716-446655440000:-:0\n"
				return stdoutPipe(strings.NewReader(output))
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
			RunStreamFn: func(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) error {
				return &transport.CommandError{
					Command:  "ssh",
					Args:     []string{"-T", host, cmd},
					ExitCode: 66,
					Stderr:   "ERROR:Remote path '/storage' does not exist.",
				}
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

	t.Run("Download delegates to RsyncRunner with IsUpload=false and tokenizes UserOptions", func(t *testing.T) {
		var capturedOpts transport.RsyncOptions
		rsync := &transport.MockRsyncRunner{
			SyncFn: func(ctx context.Context, opts transport.RsyncOptions) error {
				capturedOpts = opts
				return nil
			},
		}
		client := storage.NewClient(&transport.MockSSHRunner{}, rsync, nil, nil)

		err := client.Download(ctx, storage.TransferRequest{
			Host:        "host1",
			RemotePath:  "/remote",
			LocalPath:   "/local",
			UserOptions: "--bwlimit=5000",
		})
		require.NoError(t, err)
		assert.False(t, capturedOpts.IsUpload)
		assert.Equal(t, "host1", capturedOpts.Host)
		assert.Equal(t, "/remote", capturedOpts.RemotePath)
		assert.Equal(t, "/local", capturedOpts.LocalPath)
		assert.Equal(t, []string{"--bwlimit=5000"}, capturedOpts.ExtraArgs)
	})

	t.Run("Upload delegates to RsyncRunner with IsUpload=true and validates options", func(t *testing.T) {
		var capturedOpts transport.RsyncOptions
		rsync := &transport.MockRsyncRunner{
			SyncFn: func(ctx context.Context, opts transport.RsyncOptions) error {
				capturedOpts = opts
				return nil
			},
		}
		client := storage.NewClient(&transport.MockSSHRunner{}, rsync, nil, nil)

		err := client.Upload(ctx, storage.TransferRequest{
			Host:        "host1",
			RemotePath:  "/remote",
			LocalPath:   "/local",
			UserOptions: "--bwlimit=5000",
		})
		require.NoError(t, err)
		assert.True(t, capturedOpts.IsUpload)
		assert.Equal(t, "host1", capturedOpts.Host)
		assert.Equal(t, "/remote", capturedOpts.RemotePath)
		assert.Equal(t, "/local", capturedOpts.LocalPath)
		assert.Equal(t, []string{"--bwlimit=5000"}, capturedOpts.ExtraArgs)
	})

	t.Run("Upload rejects baseline mutating options", func(t *testing.T) {
		for _, badOpt := range []string{"--inplace", "--append", "--append-verify"} {
			client := storage.NewClient(&transport.MockSSHRunner{}, &transport.MockRsyncRunner{}, nil, nil)
			err := client.Upload(ctx, storage.TransferRequest{
				Host:        "host1",
				RemotePath:  "/remote",
				LocalPath:   "/local",
				UserOptions: badOpt,
			})
			require.Error(t, err)
			assert.Equal(t, errcode.ExitConfig, errcode.ExitCode(err))
		}
	})
}

func TestValidateUploadOptions(t *testing.T) {
	t.Parallel()

	assert.NoError(t, storage.ValidateUploadOptions([]string{"-v", "--stats", "--exclude=*.tmp"}))
	assert.Error(t, storage.ValidateUploadOptions([]string{"--inplace"}))
	assert.Error(t, storage.ValidateUploadOptions([]string{"--append"}))
	assert.Error(t, storage.ValidateUploadOptions([]string{"--append-verify"}))
}

func TestParseUsageStream(t *testing.T) {
	t.Parallel()

	input := `
TOTAL_SIZE:2.4G
COUNT:2
SCAN:1:2:550E8400-E29B-41D4-A716-446655440000
REV:550E8400-E29B-41D4-A716-446655440000:1.2G:1700000000
SCAN:2:2:c1ca82b1-7f34-4f4c-9a76-05e3297b2a23
REV:c1ca82b1-7f34-4f4c-9a76-05e3297b2a23:800M:1700001000
`

	var scanCalls []string
	totalSize, revs, err := storage.ParseUsageStream(strings.NewReader(input), func(curr, tot int, name string) {
		if name != "" {
			scanCalls = append(scanCalls, name)
		}
	})
	require.NoError(t, err)
	assert.Equal(t, "2.4G", totalSize)
	require.Len(t, revs, 2)
	assert.Equal(t, []string{"550E8400-E29B-41D4-A716-446655440000", "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23"}, scanCalls)

	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", revs[0].UUID)
	assert.Equal(t, "1.2G", revs[0].SizeHuman)
	assert.Equal(t, time.Unix(1700000000, 0), revs[0].ModTime)

	assert.Equal(t, "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23", revs[1].UUID)
	assert.Equal(t, "800M", revs[1].SizeHuman)
	assert.Equal(t, time.Unix(1700001000, 0), revs[1].ModTime)
}
