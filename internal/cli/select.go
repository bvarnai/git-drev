package cli

import (
	"context"
	"errors"
	"strings"

	"git-drev/internal/checks"
	"git-drev/internal/config"
	"git-drev/internal/domain"
	"git-drev/internal/errcode"
	"git-drev/internal/git"
	"git-drev/internal/suggest"
	"git-drev/internal/term"
	"github.com/spf13/cobra"
)

// SelectTargetKind classifies how the target argument was interpreted.
type SelectTargetKind int

const (
	SelectTargetDefault SelectTargetKind = iota
	SelectTargetRef
	SelectTargetUUID
)

// SelectOptions encapsulates validated inputs for `git-drev select`.
type SelectOptions struct {
	Target    string
	Quiet     bool
	Verbose   bool
	CallerDir string
}

// SelectResult captures the outcome of a select execution.
type SelectResult struct {
	Kind            SelectTargetKind
	RequestedTarget string
	ResolvedRef     string
}

// RunSelect executes the `git-drev select` domain logic in isolation.
func RunSelect(
	ctx context.Context,
	insp *git.Inspector,
	ops *git.Operations,
	cfg *domain.ProjectConfig,
	logger *term.Logger,
	opts SelectOptions,
) (SelectResult, error) {
	if err := ctx.Err(); err != nil {
		return SelectResult{}, errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	repoRoot, _, err := checks.InsideWorkTree(ctx, insp, opts.CallerDir)
	if err != nil {
		return SelectResult{}, err
	}

	if cfg == nil {
		loadedCfg, loadErr := config.Load(repoRoot)
		if loadErr != nil {
			return SelectResult{}, loadErr
		}
		cfg = loadedCfg
	}

	if err := ops.EnsureRemoteFetchConfig(ctx, repoRoot); err != nil {
		return SelectResult{}, err
	}

	if err := ops.FetchAll(ctx, repoRoot, opts.Quiet && !opts.Verbose); err != nil {
		return SelectResult{}, err
	}

	var kind SelectTargetKind
	var requested, resolvedRef string

	if opts.Target == "" {
		requested = cfg.Branch.Default
		if strings.TrimSpace(requested) == "" {
			requested = config.DefaultBranchName
		}
		resolvedRef = requested
		kind = SelectTargetDefault
		if logger != nil {
			logger.Infof("Selecting '%s' branch by default", requested)
		}
	} else if domain.IsValidUUID(opts.Target) {
		targetUUID, _ := domain.ParseUUID(opts.Target)
		requested = opts.Target
		kind = SelectTargetUUID
		if logger != nil {
			logger.Infof("Searching for commit with uuid '%s'", opts.Target)
		}
		commitHash, findErr := insp.FindCommitByUUID(ctx, repoRoot, targetUUID)
		if findErr != nil {
			return SelectResult{}, findErr
		}
		if logger != nil {
			logger.Infof("Found commit '%s'", commitHash)
		}
		resolvedRef = commitHash
	} else {
		requested = opts.Target
		resolvedRef = opts.Target
		kind = SelectTargetRef
	}

	if err := ops.Checkout(ctx, repoRoot, resolvedRef, false); err != nil {
		if kind == SelectTargetRef {
			candidates, _ := insp.ListAllBranchNames(ctx, repoRoot)
			tags, _ := insp.ListAllTags(ctx, repoRoot)
			candidates = append(candidates, tags...)
			if closest := suggest.Closest(requested, candidates, 3); closest != "" {
				var appErr *errcode.AppError
				if errors.As(err, &appErr) {
					appErr.WithHint("Did you mean '%s'?", closest)
				} else {
					return SelectResult{}, errcode.WrapError(errcode.ErrGitExec, err, "could not checkout '%s'", requested).
						WithHint("Did you mean '%s'?", closest)
				}
			}
		}
		return SelectResult{}, err
	}

	return SelectResult{
		Kind:            kind,
		RequestedTarget: requested,
		ResolvedRef:     resolvedRef,
	}, nil
}

func (a *App) newSelectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "select [<branch>|<tag>|<uuid>]",
		Short: "Select and check out a branch, tag, or specific revision commit",
		Long: `Selects and checks out an existing target branch, tag, or specific revision commit
resolved by UUID. If no target is supplied, it falls back to the configured default branch.`,
		ValidArgsFunction: a.completeBranchNames,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return errcode.NewError(errcode.ErrUsage, "Too many arguments: expected at most 1 target (<branch>|<tag>|<uuid>), got %d", len(args))
			}
			if len(args) == 1 && strings.HasPrefix(args[0], "-") {
				return errcode.NewError(errcode.ErrUsage, "Invalid target reference '%s': must not start with '-'", args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			opts := SelectOptions{
				Target:    target,
				Quiet:     a.Opts.Quiet,
				Verbose:   a.Opts.Verbose,
				CallerDir: a.WorkingDir,
			}
			_, err := RunSelect(cmd.Context(), a.Inspector, a.Operations, nil, a.Logger, opts)
			return err
		},
	}

	return cmd
}
