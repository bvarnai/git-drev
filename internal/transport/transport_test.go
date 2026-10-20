package transport_test

import (
	"testing"
	"time"

	"git-drev/internal/errcode"
	"git-drev/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPOSIXEscape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"", "''"},
		{"simple", "'simple'"},
		{"path/with/slashes", "'path/with/slashes'"},
		{"path with spaces", "'path with spaces'"},
		{"it's a path", "'it'\\''s a path'"},
		{`"double quotes"`, `'"double quotes"'`},
		{"$VAR; rm -rf /", "'$VAR; rm -rf /'"},
	}

	for _, tt := range tests {
		got := transport.POSIXEscape(tt.input)
		assert.Equal(t, tt.want, got, "POSIXEscape(%q)", tt.input)
	}
}

func TestTokenizeOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		want        []string
		expectError bool
	}{
		{
			name:  "empty string returns nil",
			input: "",
			want:  nil,
		},
		{
			name:  "whitespace only returns nil",
			input: "   \t\n  ",
			want:  nil,
		},
		{
			name:  "simple tokens",
			input: "-v --stats --delete",
			want:  []string{"-v", "--stats", "--delete"},
		},
		{
			name:  "tokens with double quotes preserving spaces",
			input: `--exclude="*.tmp" --exclude="foo bar"`,
			want:  []string{`--exclude=*.tmp`, `--exclude=foo bar`},
		},
		{
			name:  "tokens with single quotes preserving globs and characters",
			input: `--filter='- *.log' --bwlimit=1000`,
			want:  []string{`--filter=- *.log`, `--bwlimit=1000`},
		},
		{
			name:        "unclosed double quote returns ErrUsage",
			input:       `--exclude="unclosed`,
			expectError: true,
		},
		{
			name:        "unclosed single quote returns ErrUsage",
			input:       `--exclude='unclosed`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := transport.TokenizeOptions(tt.input)
			if tt.expectError {
				require.Error(t, err)
				assert.Equal(t, errcode.ExitUsage, errcode.ExitCode(err))
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestBuildRsyncArgs(t *testing.T) {
	t.Parallel()

	t.Run("validation of required fields", func(t *testing.T) {
		_, err := transport.BuildRsyncArgs(transport.SyncRequest{Host: "", RemotePath: "/path", LocalPath: "/local"}, true)
		require.Error(t, err)
		assert.Equal(t, errcode.ExitConfig, errcode.ExitCode(err))

		_, err = transport.BuildRsyncArgs(transport.SyncRequest{Host: "host", RemotePath: "", LocalPath: "/local"}, true)
		require.Error(t, err)
		assert.Equal(t, errcode.ExitConfig, errcode.ExitCode(err))

		_, err = transport.BuildRsyncArgs(transport.SyncRequest{Host: "host", RemotePath: "/path", LocalPath: ""}, true)
		require.Error(t, err)
		assert.Equal(t, errcode.ExitUsage, errcode.ExitCode(err))
	})

	t.Run("RsyncPut argument construction", func(t *testing.T) {
		req := transport.SyncRequest{
			Host:        "backup.example.com",
			RemotePath:  "/storage/repo/uuid/",
			LocalPath:   "/work/data/",
			Verbose:     true,
			Stats:       true,
			UserOptions: `--exclude="*.tmp" --bwlimit=5000`,
		}
		args, err := transport.BuildRsyncArgs(req, true)
		require.NoError(t, err)

		expected := []string{
			"-a",
			"--delete-during",
			"-e",
			"ssh -T",
			"-v",
			"--stats",
			`--exclude=*.tmp`,
			`--bwlimit=5000`,
			"/work/data/",
			"backup.example.com:/storage/repo/uuid/",
		}
		assert.Equal(t, expected, args)
	})

	t.Run("RsyncGet argument construction with quiet", func(t *testing.T) {
		req := transport.SyncRequest{
			Host:       "backup.example.com",
			RemotePath: "/storage/repo/uuid/",
			LocalPath:  "/work/data/",
			Quiet:      true,
		}
		args, err := transport.BuildRsyncArgs(req, false)
		require.NoError(t, err)

		expected := []string{
			"-a",
			"--delete-during",
			"-e",
			"ssh -T",
			"-q",
			"backup.example.com:/storage/repo/uuid/",
			"/work/data/",
		}
		assert.Equal(t, expected, args)
	})

	t.Run("RsyncPut rejects in-place mutation options that corrupt hardlinked baselines", func(t *testing.T) {
		for _, badOpt := range []string{"--inplace", "--append", "--append-verify"} {
			req := transport.SyncRequest{
				Host:        "backup.example.com",
				RemotePath:  "/storage/repo/uuid/",
				LocalPath:   "/work/data/",
				UserOptions: "-z " + badOpt,
			}
			_, err := transport.BuildRsyncArgs(req, true)
			require.Error(t, err, "expected %s to be rejected on put", badOpt)
			assert.Equal(t, errcode.ExitConfig, errcode.ExitCode(err))

			// Allowed on RsyncGet (local workdir is not hardlinked across revisions)
			_, getErr := transport.BuildRsyncArgs(req, false)
			require.NoError(t, getErr, "expected %s to be allowed on get", badOpt)
		}
	})
}

func TestParseQueryUsageOutput(t *testing.T) {
	t.Parallel()

	output := `
TOTAL_SIZE:2.4G
REV:550E8400-E29B-41D4-A716-446655440000:1.2G:1700000000
REV:c1ca82b1-7f34-4f4c-9a76-05e3297b2a23:800M:1700001000
`

	totalSize, revs := transport.ParseQueryUsageOutput(output)
	assert.Equal(t, "2.4G", totalSize)
	require.Len(t, revs, 2)

	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", revs[0].UUID)
	assert.Equal(t, "1.2G", revs[0].SizeHuman)
	assert.Equal(t, time.Unix(1700000000, 0), revs[0].ModTime)

	assert.Equal(t, "c1ca82b1-7f34-4f4c-9a76-05e3297b2a23", revs[1].UUID)
	assert.Equal(t, "800M", revs[1].SizeHuman)
	assert.Equal(t, time.Unix(1700001000, 0), revs[1].ModTime)
}
