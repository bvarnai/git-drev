package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/bvarnai/git-drev/internal/errcode"
)

// SSHRunner executes remote commands over SSH.
type SSHRunner interface {
	Run(ctx context.Context, host, cmd string) (string, error)
	RunStream(ctx context.Context, host, cmd string, stdin io.Reader, stdoutPipe func(io.Reader) error) error
}

// RsyncOptions holds parameters for an rsync execution.
type RsyncOptions struct {
	Host       string
	RemotePath string
	LocalPath  string
	IsUpload   bool
	Verbose    bool
	Quiet      bool
	Stats      bool
	ExtraArgs  []string
	Stdout     io.Writer
	Stderr     io.Writer
}

// RsyncRunner executes rsync synchronization.
type RsyncRunner interface {
	Sync(ctx context.Context, opts RsyncOptions) error
}

// CommandError captures the exit code, command, and output streams of a failed subprocess execution.
type CommandError struct {
	Command  string
	Args     []string
	ExitCode int
	Stdout   string
	Stderr   string
	Cause    error
}

// Error formats the failed command invocation and its stderr/stdout output.
func (e *CommandError) Error() string {
	cmdStr := e.Command
	if len(e.Args) > 0 {
		cmdStr += " " + strings.Join(e.Args, " ")
	}
	out := strings.TrimSpace(e.Stderr)
	if out == "" {
		out = strings.TrimSpace(e.Stdout)
	}
	if out != "" {
		return fmt.Sprintf("%s failed (exit %d): %s", cmdStr, e.ExitCode, out)
	}
	return fmt.Sprintf("%s failed (exit %d)", cmdStr, e.ExitCode)
}

// Unwrap allows errors.Is(err, errcode.ErrTransport) to match CommandError.
func (e *CommandError) Unwrap() error {
	return errcode.ErrTransport
}

// ExitCodeOf extracts the process exit code from err if it wraps a *CommandError,
// or returns (-1, false) otherwise.
func ExitCodeOf(err error) (int, bool) {
	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.ExitCode, true
	}
	return -1, false
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
