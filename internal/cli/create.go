package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bvarnai/git-drev/internal/checks"
	"github.com/bvarnai/git-drev/internal/config"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/term"
	"github.com/spf13/cobra"
)

// CreateOptions holds validated inputs for `git-drev create`.
type CreateOptions struct {
	Branch    string
	Verbose   bool
	Quiet     bool
	CallerDir string
}

// CreateAction classifies the action performed by create.
type CreateAction string

const (
	CreateActionCheckedOutLocal CreateAction = "checked_out_local"
	CreateActionSwitchedRemote  CreateAction = "switched_remote"
	CreateActionCreatedNew      CreateAction = "created_new"
)

// CreateResult captures the outcome of RunCreate.
type CreateResult struct {
	Branch     string
	Action     CreateAction
	MarkerUUID string
}

// RunCreate executes the `git-drev create` domain logic in isolation.
func RunCreate(
	ctx context.Context,
	insp *git.Inspector,
	ops *git.Operations,
	cfg *domain.ProjectConfig,
	logger *term.Logger,
	opts CreateOptions,
) (*CreateResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	timer := term.StartTimer()

	repoRoot, _, err := checks.InsideWorkTree(ctx, insp, opts.CallerDir)
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

	if err := insp.ValidateBranchName(ctx, repoRoot, opts.Branch); err != nil {
		return nil, err
	}

	if err := ops.EnsureRemoteFetchConfig(ctx, repoRoot); err != nil {
		return nil, err
	}

	// 1. Check if branch exists locally
	if logger != nil {
		logger.Info("Checking for existing branch (local)")
	}
	existsLocal, err := insp.LocalBranchExists(ctx, repoRoot, opts.Branch)
	if err != nil {
		return nil, err
	}
	if existsLocal {
		if logger != nil {
			logger.Infof("Branch '%s' found (local)", opts.Branch)
		}
		if err := ops.FetchAll(ctx, repoRoot, opts.Quiet && !opts.Verbose); err != nil {
			return nil, err
		}
		if err := ops.Checkout(ctx, repoRoot, opts.Branch, false); err != nil {
			return nil, err
		}
		if logger != nil {
			logger.Info(timer.String())
		}
		return &CreateResult{Branch: opts.Branch, Action: CreateActionCheckedOutLocal}, nil
	}
	if logger != nil {
		logger.Infof("No branch '%s' found (local)", opts.Branch)
	}

	// 2. Check if branch exists on origin
	if logger != nil {
		logger.Info("Checking for existing branch (remote)")
	}
	existsRemote, err := insp.RemoteBranchExists(ctx, repoRoot, opts.Branch)
	if err != nil {
		return nil, err
	}
	if existsRemote {
		if logger != nil {
			logger.Infof("Branch '%s' found (remote)", opts.Branch)
		}
		if err := ops.FetchAll(ctx, repoRoot, opts.Quiet && !opts.Verbose); err != nil {
			return nil, err
		}
		if err := ops.Switch(ctx, repoRoot, opts.Branch); err != nil {
			return nil, err
		}
		if logger != nil {
			logger.Info(timer.String())
		}
		return &CreateResult{Branch: opts.Branch, Action: CreateActionSwitchedRemote}, nil
	}
	if logger != nil {
		logger.Infof("No branch '%s' found (remote)", opts.Branch)
	}

	// 3. Resolve parent revision UUID from current HEAD
	parentUUID := domain.ZeroUUID
	if meta, headErr := insp.HeadCommitMeta(ctx, repoRoot); headErr == nil {
		if meta.UUID != "" && domain.IsValidUUID(meta.UUID) {
			parentUUID = strings.ToLower(meta.UUID)
		}
	}
	if logger != nil {
		logger.Debugf("Resolved parent revision UUID: %s", parentUUID)
	}

	// Record starting state for transactional rollback
	origBranch, origDetached, _ := insp.CurrentBranch(ctx, repoRoot)
	origHeadHash, _ := insp.HeadHash(ctx, repoRoot)
	branchCreatedLocally := false

	defer func() {
		if branchCreatedLocally {
			if logger != nil {
				logger.Debugf("Rolling back locally created branch '%s' after failed remote push", opts.Branch)
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if origDetached && origHeadHash != "" {
				_ = ops.Checkout(cleanupCtx, repoRoot, origHeadHash, false)
			} else if origBranch != "" {
				_ = ops.Checkout(cleanupCtx, repoRoot, origBranch, false)
			}
			_ = ops.DeleteLocalBranch(cleanupCtx, repoRoot, opts.Branch)
		}
	}()

	// 4. Create new local branch
	if logger != nil {
		logger.Infof("Creating new revision branch '%s'", opts.Branch)
	}
	if err := ops.CheckoutNewBranch(ctx, repoRoot, opts.Branch); err != nil {
		return nil, err
	}
	branchCreatedLocally = true

	// 5. Commit empty revision marker
	if logger != nil {
		logger.Info("Committing empty revision marker")
	}
	markerJSON := fmt.Sprintf(`{"uuid":%q,"marker":"true"}`, parentUUID)
	if err := ops.CommitAllowEmpty(ctx, repoRoot, markerJSON); err != nil {
		return nil, err
	}

	// 6. Push new branch to origin
	if logger != nil {
		logger.Infof("Pushing new revision branch '%s'", opts.Branch)
	}
	upstream, _ := insp.BranchUpstream(ctx, repoRoot, opts.Branch)
	hasUpstream := upstream != ""
	if _, _, err := ops.PushForceWithLease(ctx, repoRoot, opts.Branch, !hasUpstream); err != nil {
		return nil, err
	}

	// Push succeeded: disarm rollback
	branchCreatedLocally = false

	if logger != nil {
		logger.Info(timer.String())
	}

	return &CreateResult{
		Branch:     opts.Branch,
		Action:     CreateActionCreatedNew,
		MarkerUUID: parentUUID,
	}, nil
}

func (a *App) newCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <branch>",
		Short: "Create a new revision branch with an initial marker commit",
		Long: `Creates a new revision metadata branch at HEAD with an initial marker commit
carrying the parent revision's UUID (or ZeroUUID) and pushes it to origin,
or fetches and switches to <branch> if it already exists locally or remotely.`,
		ValidArgsFunction: a.completeBranchNames,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errcode.NewError(errcode.ErrUsage, "No branch name specified")
			}
			if len(args) > 1 {
				return errcode.NewError(errcode.ErrUsage, "Unexpected argument '%s'", args[1])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := CreateOptions{
				Branch:    args[0],
				Verbose:   a.Opts.Verbose,
				Quiet:     a.Opts.Quiet,
				CallerDir: a.WorkingDir,
			}
			_, err := RunCreate(cmd.Context(), a.Inspector, a.Operations, nil, a.Logger, opts)
			return err
		},
	}

	return cmd
}
