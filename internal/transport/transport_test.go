package transport_test

import (
	"testing"

	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/transport"
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
		_, err := transport.BuildRsyncArgs(transport.RsyncOptions{Host: "", RemotePath: "/path", LocalPath: "/local"})
		require.Error(t, err)
		assert.Equal(t, errcode.ExitConfig, errcode.ExitCode(err))

		_, err = transport.BuildRsyncArgs(transport.RsyncOptions{Host: "host", RemotePath: "", LocalPath: "/local"})
		require.Error(t, err)
		assert.Equal(t, errcode.ExitConfig, errcode.ExitCode(err))

		_, err = transport.BuildRsyncArgs(transport.RsyncOptions{Host: "host", RemotePath: "/path", LocalPath: ""})
		require.Error(t, err)
		assert.Equal(t, errcode.ExitUsage, errcode.ExitCode(err))
	})

	t.Run("Rsync upload argument construction", func(t *testing.T) {
		opts := transport.RsyncOptions{
			Host:       "backup.example.com",
			RemotePath: "/storage/repo/uuid/",
			LocalPath:  "/work/data/",
			IsUpload:   true,
			Verbose:    true,
			Stats:      true,
			ExtraArgs:  []string{`--exclude=*.tmp`, `--bwlimit=5000`},
		}
		args, err := transport.BuildRsyncArgs(opts)
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

	t.Run("Rsync download argument construction with quiet", func(t *testing.T) {
		opts := transport.RsyncOptions{
			Host:       "backup.example.com",
			RemotePath: "/storage/repo/uuid/",
			LocalPath:  "/work/data/",
			IsUpload:   false,
			Quiet:      true,
		}
		args, err := transport.BuildRsyncArgs(opts)
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
}
