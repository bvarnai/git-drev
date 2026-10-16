// Package transport provides SSH and rsync remote execution engines for git-drev.
package transport

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"git-drev/internal/domain"
	"git-drev/internal/errcode"
)

// SyncRequest configures an rsync transfer operation.
type SyncRequest struct {
	Host        string
	RemotePath  string
	LocalPath   string
	Verbose     bool
	Quiet       bool
	Stats       bool
	UserOptions string
}

// RemoteRunner abstracts SSH and rsync operations against the remote storage host.
type RemoteRunner interface {
	CheckDirExists(ctx context.Context, host, remotePath string) (bool, error)
	CloneBaseline(ctx context.Context, host, srcPath, dstPath string) error
	RemoveDir(ctx context.Context, host, remotePath string) error
	QueryUsage(ctx context.Context, host, basePath string) (totalSize string, revisions []domain.RemoteRevision, err error)
	RsyncGet(ctx context.Context, req SyncRequest) error
	RsyncPut(ctx context.Context, req SyncRequest) error
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

// BuildRsyncArgs constructs deterministic command-line arguments for rsync.
func BuildRsyncArgs(req SyncRequest, isPut bool) ([]string, error) {
	if req.Host == "" {
		return nil, errcode.NewError(errcode.ErrConfig, "remote host is not configured")
	}
	if req.RemotePath == "" {
		return nil, errcode.NewError(errcode.ErrConfig, "remote path is not configured")
	}
	if req.LocalPath == "" {
		return nil, errcode.NewError(errcode.ErrUsage, "local path is not specified")
	}

	args := []string{
		"-a",
		"--delete-during",
		"-e",
		"ssh -T",
	}

	if req.Verbose {
		args = append(args, "-v")
	}
	if req.Quiet {
		args = append(args, "-q")
	}
	if req.Stats {
		args = append(args, "--stats")
	}

	if strings.TrimSpace(req.UserOptions) != "" {
		userOpts, err := TokenizeOptions(req.UserOptions)
		if err != nil {
			return nil, err
		}
		args = append(args, userOpts...)
	}

	remoteTarget := fmt.Sprintf("%s:%s", req.Host, req.RemotePath)

	if isPut {
		args = append(args, req.LocalPath, remoteTarget)
	} else {
		args = append(args, remoteTarget, req.LocalPath)
	}

	return args, nil
}
