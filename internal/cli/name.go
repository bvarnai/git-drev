package cli

import (
	"context"
	"fmt"
	"io"

	"git-drev/internal/errcode"
	"git-drev/internal/git"
	"git-drev/internal/term"
	"github.com/spf13/cobra"
)

// DetachedHeadDisplay is the canonical output written to stdout when in detached HEAD state.
const DetachedHeadDisplay = "No branch (detached HEAD state)"

// NameOptions encapsulates parameters for the `name` subcommand.
type NameOptions struct {
	Quiet     bool
	CallerDir string
}

// NameResult captures the resolved branch state.
type NameResult struct {
	Branch   string
	Detached bool
}

func (r NameResult) String() string {
	if r.Detached {
		return DetachedHeadDisplay
	}
	return r.Branch
}

// RunName executes the `git-drev name` domain logic in isolation.
func RunName(ctx context.Context, insp *git.Inspector, logger *term.Logger, stdout io.Writer, opts NameOptions) (NameResult, error) {
	if err := ctx.Err(); err != nil {
		return NameResult{}, errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	repoRoot, _, err := insp.RepoRoot(ctx, opts.CallerDir)
	if err != nil {
		return NameResult{}, err
	}

	branch, detached, err := insp.CurrentBranch(ctx, repoRoot)
	if err != nil {
		return NameResult{}, err
	}

	res := NameResult{
		Branch:   branch,
		Detached: detached,
	}

	if _, writeErr := fmt.Fprintln(stdout, res.String()); writeErr != nil {
		return NameResult{}, errcode.WrapError(errcode.ErrRuntime, writeErr, "Failed to write output")
	}

	return res, nil
}

func (a *App) newNameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "name",
		Short: "Print the current branch name or detached HEAD status",
		Long: `Queries the local Git repository and prints the name of the active branch
(or 'No branch (detached HEAD state)') to standard output.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return errcode.NewError(errcode.ErrUsage, "This command takes no arguments (got: %q)", args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := NameOptions{
				Quiet:     a.Opts.Quiet,
				CallerDir: a.WorkingDir,
			}
			_, err := RunName(cmd.Context(), a.Inspector, a.Logger, cmd.OutOrStdout(), opts)
			return err
		},
	}

	return cmd
}
