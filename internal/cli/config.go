package cli

import (
	"context"
	"fmt"
	"io"

	"git-drev/internal/config"
	"git-drev/internal/errcode"
	"git-drev/internal/git"
	"git-drev/internal/term"
	"github.com/spf13/cobra"
)

// ConfigOptions holds flags for `git-drev config`.
type ConfigOptions struct {
	Sources bool
}

// RunConfig executes the config inspection and formatting.
func RunConfig(ctx context.Context, insp *git.Inspector, logger *term.Logger, stdout io.Writer, opts ConfigOptions) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	targetDir := ""
	if insp != nil {
		if root, _, err := insp.RepoRoot(ctx, ""); err == nil {
			targetDir = root
		}
	}

	report, err := config.Inspect(targetDir)
	if err != nil {
		return err
	}

	formatted, err := config.FormatReport(report, opts.Sources)
	if err != nil {
		return errcode.WrapError(errcode.ErrConfig, err, "failed to format configuration report")
	}

	if _, err := fmt.Fprint(stdout, formatted); err != nil {
		return errcode.WrapError(errcode.ErrRuntime, err, "failed to write output")
	}

	return nil
}

func (a *App) newConfigCmd() *cobra.Command {
	opts := ConfigOptions{}

	cmd := &cobra.Command{
		Use:   "config",
		Short: "Display effective configuration across the 3-tier cascade",
		Long: `Displays the fully resolved git-drev configuration with source provenance
and inline annotations for defaulted or inferred fields.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return errcode.NewError(errcode.ErrUsage, "Unexpected argument: %s", args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return RunConfig(cmd.Context(), a.Inspector, a.Logger, cmd.OutOrStdout(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Sources, "sources", false, "Output only the resolved configuration sources hierarchy")
	return cmd
}
