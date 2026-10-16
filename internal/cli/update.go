package cli

import (
	"context"

	"git-drev/internal/config"
	"git-drev/internal/domain"
	"git-drev/internal/errcode"
	"git-drev/internal/git"
	"git-drev/internal/term"
	"github.com/spf13/cobra"
)

// UpdateOptions encapsulates validated parameters for `git-drev update`.
type UpdateOptions struct {
	Verbose bool
	Quiet   bool
}

// UpdateResult captures the outcome of an update execution.
type UpdateResult struct {
	Branch      string
	UpstreamRef string
}

// RunUpdate executes the `git-drev update` workflow.
func RunUpdate(
	ctx context.Context,
	insp *git.Inspector,
	ops *git.Operations,
	cfg *domain.ProjectConfig,
	logger *term.Logger,
	opts UpdateOptions,
) (*UpdateResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	timer := term.StartTimer()

	repoRoot, _, err := insp.RepoRoot(ctx, "")
	if err != nil {
		return nil, err
	}

	if cfg == nil {
		loadedCfg, loadErr := config.Load(repoRoot)
		if loadErr != nil {
			return nil, loadErr
		}
		cfg = loadedCfg
	}

	branch, detached, err := insp.CurrentBranch(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	if detached {
		return nil, errcode.NewError(errcode.ErrDetachedHead, "Detached HEAD state detected, aborting").
			WithHint(`Use "git-drev select <branch>" to select a branch first.`)
	}

	if err := ops.EnsureRemoteFetchConfig(ctx, repoRoot); err != nil {
		return nil, err
	}

	if logger != nil {
		logger.Infof("Updating branch '%s' to origin/%s", branch, branch)
	}

	if err := ops.FetchAll(ctx, repoRoot, opts.Quiet && !opts.Verbose); err != nil {
		return nil, err
	}

	upstreamRef := "refs/remotes/origin/" + branch
	exists, err := insp.RemoteTrackingBranchExists(ctx, repoRoot, branch)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errcode.NewError(errcode.ErrGitState, "Remote branch 'origin/%s' does not exist", branch)
	}

	if err := ops.ResetHard(ctx, repoRoot, upstreamRef); err != nil {
		return nil, err
	}

	if logger != nil {
		logger.Info(timer.String())
	}

	return &UpdateResult{
		Branch:      branch,
		UpstreamRef: upstreamRef,
	}, nil
}

func (a *App) newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Fetch and hard-reset the current branch to origin/<branch>",
		Long: `Ensures all remote branches are tracked, fetches the latest refs from origin,
and hard-resets the active local branch to match its origin tracking branch.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return errcode.NewError(errcode.ErrUsage, "update takes no positional arguments")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := UpdateOptions{
				Verbose: a.Opts.Verbose,
				Quiet:   a.Opts.Quiet,
			}
			_, err := RunUpdate(cmd.Context(), a.Inspector, a.Operations, nil, a.Logger, opts)
			return err
		},
	}

	return cmd
}
