package transport

import (
	"context"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/bvarnai/git-drev/internal/errcode"
)

// SSHRunner executes remote commands over SSH.
type SSHRunner interface {
	Run(ctx context.Context, host, cmd string) (stdout string, stderr string, err error)
	RunStream(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) (stderr string, err error)
}

// RsyncOptions holds parameters for an rsync execution.
type RsyncOptions struct {
	Host        string
	RemotePath  string
	LocalPath   string
	IsUpload    bool
	Verbose     bool
	Quiet       bool
	Stats       bool
	UserOptions string
	Stdout      io.Writer
	Stderr      io.Writer
}

// RsyncRunner executes rsync synchronization.
type RsyncRunner interface {
	Sync(ctx context.Context, opts RsyncOptions) error
}

// POSIXEscape returns a single-quoted POSIX-safe representation of s.
func POSIXEscape(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// TokenizeOptions parses a shell options string into discrete tokens without shell eval or globbing.
// Supports single and double quotes and escaped quotes.
func TokenizeOptions(input string) ([]string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, nil
	}

	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	inToken := false

	for _, r := range trimmed {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}

		if inSingle {
			if r == '\'' {
				inSingle = false
			} else {
				current.WriteRune(r)
			}
			continue
		}

		if inDouble {
			switch r {
			case '"':
				inDouble = false
			case '\\':
				escaped = true
			default:
				current.WriteRune(r)
			}
			continue
		}

		switch {
		case r == '\\':
			escaped = true
			inToken = true
		case r == '\'':
			inSingle = true
			inToken = true
		case r == '"':
			inDouble = true
			inToken = true
		case unicode.IsSpace(r):
			if inToken {
				tokens = append(tokens, current.String())
				current.Reset()
				inToken = false
			}
		default:
			current.WriteRune(r)
			inToken = true
		}
	}

	if inSingle || inDouble || escaped {
		return nil, errcode.NewError(errcode.ErrUsage, "unclosed quote or trailing escape in options: %s", input)
	}

	if inToken {
		tokens = append(tokens, current.String())
	}

	return tokens, nil
}

// SanitizedSubprocessEnv returns a sanitized environment slice for subprocess execution.
func SanitizedSubprocessEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		k, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		switch k {
		case "LC_ALL", "LANG", "GIT_TERMINAL_PROMPT", "GIT_ADVICE":
			continue
		default:
			env = append(env, entry)
		}
	}
	env = append(env,
		"LC_ALL=C",
		"LANG=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ADVICE=0",
	)
	return env
}
