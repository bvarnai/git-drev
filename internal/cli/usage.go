package cli

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/bvarnai/git-drev/internal/config"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/bvarnai/git-drev/internal/term"
	"github.com/spf13/cobra"
)

var uuidRegex = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// UsageOptions holds options for `git-drev usage`.
type UsageOptions struct {
	All       bool
	Verbose   bool
	Quiet     bool
	CallerDir string
}

// UsageResult captures the parsed outcome of `git-drev usage`.
type UsageResult struct {
	TotalSize     string
	RevisionCount int
	Revisions     []UsageRevisionRow
}

// UsageRevisionRow describes a single revision in the verbose table.
type UsageRevisionRow struct {
	UUID      string
	SizeHuman string
	Created   string
	Commit    string
	BranchTag string
	Message   string
	Orphaned  bool
}

// RunUsage queries storage usage on the remote server and correlates with Git history.
func RunUsage(
	ctx context.Context,
	insp *git.Inspector,
	ops *git.Operations,
	stor storage.Manager,
	cfg *domain.ProjectConfig,
	logger *term.Logger,
	stdout io.Writer,
	opts UsageOptions,
) (UsageResult, error) {
	if err := ctx.Err(); err != nil {
		return UsageResult{}, errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	repoRoot, _, err := insp.RepoRoot(ctx, opts.CallerDir)
	if err != nil {
		return UsageResult{}, err
	}

	if cfg == nil {
		loadedCfg, loadErr := config.Load(repoRoot)
		if loadErr != nil {
			return UsageResult{}, loadErr
		}
		cfg = loadedCfg
	}

	if cfg.Remote.Host == "" {
		return UsageResult{}, errcode.NewError(errcode.ErrConfig, "Remote host is not configured")
	}
	if cfg.Remote.Path == "" {
		return UsageResult{}, errcode.NewError(errcode.ErrConfig, "Remote path is not configured")
	}

	if err := ops.EnsureRemoteFetchConfig(ctx, repoRoot); err != nil {
		return UsageResult{}, err
	}

	var spinner *term.Spinner
	if logger != nil {
		spinner = logger.StartSpinner(fmt.Sprintf("Connecting to host '%s'...", cfg.Remote.Host))
		defer spinner.Stop()
	}

	basePath := strings.TrimRight(cfg.Remote.Path, "/")
	showDetails := opts.All
	if logger != nil {
		logger.Debugf("Querying remote storage on host '%s', path '%s' (detailed=%v)", cfg.Remote.Host, basePath, showDetails)
	}
	totalSize, revisions, queryErr := stor.QueryUsage(ctx, cfg.Remote.Host, basePath, storage.QueryUsageOptions{
		Detailed: showDetails,
		Verbose:  showDetails,
		OnProgress: func(current, total int, uuid string) {
			if spinner == nil {
				return
			}
			if current == 0 {
				if total > 0 {
					spinner.Update(fmt.Sprintf("Calculating total storage usage across %d revision(s)...", total))
				} else {
					spinner.Update("Calculating total storage usage...")
				}
				return
			}
			shortUUID := uuid
			if len(shortUUID) > 8 {
				shortUUID = shortUUID[:8]
			}
			spinner.Update(fmt.Sprintf("Scanning revision %d/%d (%s)...", current, total, shortUUID))
		},
	})
	if spinner != nil {
		spinner.Stop()
	}
	if queryErr != nil {
		return UsageResult{}, queryErr
	}

	if logger != nil {
		logger.Debugf("Storage discovery completed: %d revision(s) found, total space: %s", len(revisions), totalSize)
		logger.Infof("Retrieved storage usage from host '%s'", cfg.Remote.Host)
	}

	// Render summary to stdout
	fmt.Fprintf(stdout, "\nSummary:\n  Remote storage:    %s:%s\n  Total space used:  %s\n  Revisions found:   %d\n\n",
		cfg.Remote.Host, cfg.Remote.Path, totalSize, len(revisions))

	res := UsageResult{
		TotalSize:     totalSize,
		RevisionCount: len(revisions),
	}

	if len(revisions) == 0 {
		if logger != nil {
			logger.Info("No revision directories found on remote storage.")
		}
		return res, nil
	}

	if !showDetails {
		if logger != nil {
			logger.HintAlways(`Use -a/--all to view the detailed breakdown of revisions.`)
		}
		return res, nil
	}

	// Correlate with Git history
	commits, _ := insp.ScanAllCommits(ctx, repoRoot, false)
	byUUID := make(map[string]git.CommitRecord)

	for _, rec := range commits {
		var matchedUUID string
		if rec.Meta != nil && rec.Meta.UUID != "" && rec.Meta.UUID != domain.ZeroUUID && !rec.Meta.IsMarker() {
			matchedUUID = strings.ToLower(rec.Meta.UUID)
		} else {
			if sub := uuidRegex.FindString(rec.Subject); sub != "" {
				matchedUUID = strings.ToLower(sub)
			}
		}

		if matchedUUID != "" {
			if _, exists := byUUID[matchedUUID]; !exists {
				byUUID[matchedUUID] = rec
			}
		}
	}

	var rows []UsageRevisionRow
	activeCount := 0
	orphanedCount := 0
	for _, rev := range revisions {
		created := "-"
		if !rev.ModTime.IsZero() {
			created = rev.ModTime.Format("2006-01-02 15:04:05")
		}

		normUUID := strings.ToLower(rev.UUID)
		rec, found := byUUID[normUUID]

		var commitStr, branchTagStr, msgStr string
		var orphaned bool

		if found {
			activeCount++
			orphaned = false
			commitStr = rec.ShortHash
			branchTagStr = formatRefs(rec.Refs)
			if rec.Meta != nil && rec.Meta.Seq != "" {
				msgStr = "seq: " + rec.Meta.Seq
			} else {
				msgStr = rec.Subject
				if msgStr == "" {
					msgStr = "-"
				}
			}
		} else {
			orphanedCount++
			orphaned = true
			commitStr = "[orphaned]"
			branchTagStr = "-"
			msgStr = "-"
		}

		rows = append(rows, UsageRevisionRow{
			UUID:      normUUID,
			SizeHuman: rev.SizeHuman,
			Created:   created,
			Commit:    commitStr,
			BranchTag: branchTagStr,
			Message:   msgStr,
			Orphaned:  orphaned,
		})
	}

	if logger != nil {
		logger.Debugf("Correlated %d remote revision(s) against %d Git commit(s): %d active, %d orphaned", len(revisions), len(commits), activeCount, orphanedCount)
	}

	res.Revisions = rows

	// Write verbose breakdown table
	fmt.Fprintln(stdout, "Revisions breakdown:")
	fmt.Fprintf(stdout, "%-36s  %8s  %-19s  %-10s  %-20s  %s\n", "UUID", "Size", "Created", "Commit", "Branch/Tag", "Message")
	fmt.Fprintf(stdout, "%-36s  %8s  %-19s  %-10s  %-20s  %s\n",
		"------------------------------------",
		"--------",
		"-------------------",
		"----------",
		"--------------------",
		"----------------------")

	for _, row := range rows {
		fmt.Fprintf(stdout, "%-36s  %8s  %-19s  %-10s  %-20s  %s\n",
			row.UUID, row.SizeHuman, row.Created, row.Commit, row.BranchTag, row.Message)
	}

	return res, nil
}

func formatRefs(refs []string) string {
	var clean []string
	for _, r := range refs {
		r = strings.TrimSpace(r)
		r = strings.TrimPrefix(r, "HEAD -> ")
		r = strings.TrimPrefix(r, "tag: ")
		r = strings.TrimSpace(r)
		if r == "" || r == "origin/HEAD" {
			continue
		}
		clean = append(clean, r)
	}
	if len(clean) == 0 {
		return "-"
	}
	return strings.Join(clean, ", ")
}

func (a *App) newUsageCmd() *cobra.Command {
	opts := UsageOptions{}

	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Display storage consumption of revisions on the remote host",
		Long: `Queries the remote storage host via SSH to display total space consumption
and, when invoked with -a/--all, correlates remote revision directories with Git commits.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return errcode.NewError(errcode.ErrUsage, "usage takes no arguments (got: %s)", args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Verbose = a.Opts.Verbose
			opts.Quiet = a.Opts.Quiet
			opts.CallerDir = a.WorkingDir
			_, err := RunUsage(
				cmd.Context(),
				a.Inspector,
				a.Operations,
				a.Storage,
				nil,
				a.Logger,
				cmd.OutOrStdout(),
				opts,
			)
			return err
		},
	}

	cmd.Flags().BoolVarP(&opts.All, "all", "a", false, "Display detailed breakdown of all revisions")
	return cmd
}
