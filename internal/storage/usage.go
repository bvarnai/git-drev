package storage

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/transport"
)

// QueryUsageOptions configures options and progress callbacks for QueryUsage.
type QueryUsageOptions struct {
	Verbose    bool
	OnProgress func(current, total int, uuid string)
}

const queryUsageScript = `set -e
target="$1"
mode="$2"
if [ ! -d "$target" ]; then
    echo "ERROR:Remote path '$target' does not exist." >&2
    exit 66
fi

rev_dirs=""
count=0
for d in "$target"/*; do
    [ -d "$d" ] || continue
    name=$(basename -- "$d")
    case "$name" in
        [0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]-[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]-[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]-[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]-[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F])
            count=$((count + 1))
            rev_dirs="${rev_dirs}${d}
"
            ;;
    esac
done

echo "COUNT:${count}"
total_size=$(du -sh -- "$target" 2>/dev/null | cut -f1)
echo "TOTAL_SIZE:${total_size:--}"

if [ "$mode" != "verbose" ]; then
    printf "%s" "$rev_dirs" | while IFS= read -r d; do
        [ -n "$d" ] || continue
        name=$(basename -- "$d")
        echo "REV:${name}:-:0"
    done
    exit 0
fi

idx=0
printf "%s" "$rev_dirs" | while IFS= read -r d; do
    [ -n "$d" ] || continue
    idx=$((idx + 1))
    name=$(basename -- "$d")
    echo "SCAN:${idx}:${count}:${name}"
    size=$(du -sh -- "$d" 2>/dev/null | cut -f1)
    mtime=$(stat -c %Y -- "$d" 2>/dev/null || stat -f %m -- "$d" 2>/dev/null || echo 0)
    echo "REV:${name}:${size:--}:${mtime:-0}:${idx}:${count}"
done
`

// QueryUsage executes remote storage discovery and returns total size and discovered revision directories.
func (c *Client) QueryUsage(ctx context.Context, host, basePath string, opts ...QueryUsageOptions) (string, []domain.RemoteRevision, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, errcode.WrapError(errcode.ErrInterrupted, err, "operation interrupted")
	}

	var opt QueryUsageOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	mode := "fast"
	if opt.Verbose {
		mode = "verbose"
	}

	cmdStr := fmt.Sprintf("/bin/sh -c %s -- %s %s",
		transport.POSIXEscape(queryUsageScript),
		transport.POSIXEscape(basePath),
		mode,
	)

	var lines []string
	var totalRevs int

	errStr, runErr := c.ssh.RunStream(ctx, host, cmdStr, nil, func(stdout io.Reader) error {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			lines = append(lines, line)

			if strings.HasPrefix(line, "COUNT:") {
				if n, err := strconv.Atoi(strings.TrimPrefix(line, "COUNT:")); err == nil {
					totalRevs = n
					if opt.OnProgress != nil {
						opt.OnProgress(0, totalRevs, "")
					}
				}
			} else if strings.HasPrefix(line, "SCAN:") && opt.OnProgress != nil {
				parts := strings.SplitN(strings.TrimPrefix(line, "SCAN:"), ":", 3)
				if len(parts) == 3 {
					curr, _ := strconv.Atoi(parts[0])
					tot, _ := strconv.Atoi(parts[1])
					opt.OnProgress(curr, tot, parts[2])
				}
			}
		}
		return scanner.Err()
	})

	if runErr != nil {
		if ctx.Err() != nil {
			return "", nil, errcode.WrapError(errcode.ErrInterrupted, ctx.Err(), "operation interrupted")
		}

		var ec exitCoder
		if (errors.As(runErr, &ec) && ec.ExitCode() == 66) || strings.Contains(errStr, "ERROR:Remote path") {
			return "", nil, errcode.NewError(errcode.ErrRemoteNotFound, "remote path %q does not exist on host %q", basePath, host)
		}

		errMsg := strings.TrimSpace(errStr)
		if errMsg == "" {
			errMsg = runErr.Error()
		}
		return "", nil, errcode.NewError(errcode.ErrTransport, "SSH usage query failed on host %q: %s", host, errMsg)
	}

	totalSize, revisions := ParseQueryUsageOutput(strings.Join(lines, "\n"))
	return totalSize, revisions, nil
}

// ParseQueryUsageOutput parses standard stdout emitted by queryUsageScript.
func ParseQueryUsageOutput(output string) (totalSize string, revisions []domain.RemoteRevision) {
	totalSize = "-"
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "TOTAL_SIZE:") {
			totalSize = strings.TrimPrefix(line, "TOTAL_SIZE:")
			if totalSize == "" {
				totalSize = "-"
			}
			continue
		}
		if strings.HasPrefix(line, "REV:") {
			parts := strings.Split(strings.TrimPrefix(line, "REV:"), ":")
			if len(parts) >= 2 {
				uuid := strings.ToLower(parts[0])
				size := parts[1]
				var mtime time.Time
				if len(parts) >= 3 {
					if epoch, err := strconv.ParseInt(parts[2], 10, 64); err == nil && epoch > 0 {
						mtime = time.Unix(epoch, 0)
					}
				}
				revisions = append(revisions, domain.RemoteRevision{
					UUID:      uuid,
					SizeHuman: size,
					ModTime:   mtime,
				})
			}
		}
	}
	return totalSize, revisions
}
