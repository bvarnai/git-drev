package cli

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/bvarnai/git-drev/internal/config"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/hook"
	"github.com/bvarnai/git-drev/internal/term"
	"github.com/bvarnai/git-drev/internal/version"
	"github.com/spf13/cobra"
)

// InfoOptions encapsulates inputs for `git-drev info`.
type InfoOptions struct {
	Quiet       bool
	Verbose     bool
	HookTimeout time.Duration
	CallerDir   string
}

// InfoResult captures the outcome of running info.
type InfoResult struct {
	HookFound    bool
	HookExecuted bool
	HookPath     string
	Env          hook.Env
}

// RunInfo executes repository inspection and .git-drev-info-hook.
func RunInfo(
	ctx context.Context,
	insp *git.Inspector,
	hooks hook.Runner,
	cfg *domain.ProjectConfig,
	logger *term.Logger,
	stdout, stderr io.Writer,
	opts InfoOptions,
) (InfoResult, error) {
	if err := ctx.Err(); err != nil {
		return InfoResult{}, errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	repoRoot, _, err := insp.RepoRoot(ctx, opts.CallerDir)
	if err != nil {
		return InfoResult{}, err
	}

	if cfg == nil {
		loadedCfg, loadErr := config.Load(repoRoot)
		if loadErr != nil {
			return InfoResult{}, loadErr
		}
		cfg = loadedCfg
	}

	hookFile := hook.HookFileName(hook.KindInfo)
	hookPath := filepath.Join(repoRoot, hookFile)

	fi, statErr := os.Stat(hookPath)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			if _, lerr := os.Lstat(hookPath); lerr == nil {
				return InfoResult{}, errcode.NewError(errcode.ErrRuntime, "Info hook %q is a broken symlink", hookFile)
			}
			if logger != nil {
				logger.Debugf("Checked for info hook at '%s': not found", hookPath)
				logger.Info("No info hook found")
			}
			return InfoResult{HookFound: false, HookPath: hookPath}, nil
		}
		return InfoResult{}, errcode.WrapError(errcode.ErrRuntime, statErr, "Failed to stat info hook %q", hookPath)
	}

	if fi.IsDir() {
		return InfoResult{}, errcode.NewError(errcode.ErrRuntime, "Info hook path %q is a directory, expected file", hookPath)
	}

	// Resolve Git context for hook env
	branch, detached, branchErr := insp.CurrentBranch(ctx, repoRoot)
	if branchErr != nil {
		return InfoResult{}, branchErr
	}
	if detached {
		branch = "HEAD"
	}

	var uuidStr, seqStr string
	if meta, headErr := insp.HeadCommitMeta(ctx, repoRoot); headErr == nil {
		uuidStr = meta.UUID
		seqStr = meta.Seq
	} else if logger != nil {
		logger.Debugf("HEAD commit has no drev metadata (%v); falling back to empty UUID and sequence", headErr)
	}

	hookEnv := hook.Env{
		Version: version.String(),
		UUID:    uuidStr,
		Seq:     seqStr,
		Branch:  branch,
		WorkDir: filepath.Join(repoRoot, cfg.WorkDir),
		Root:    repoRoot,
	}

	timeout := opts.HookTimeout
	if timeout <= 0 {
		timeout = hook.DefaultTimeout
	}

	runReq := hook.RunRequest{
		Kind:    hook.KindInfo,
		Path:    hookPath,
		WorkDir: repoRoot,
		Env:     hookEnv,
		Timeout: timeout,
		Stdout:  stdout,
		Stderr:  stderr,
	}

	if logger != nil {
		logger.Debugf("Executing info hook '%s' (branch: '%s', uuid: '%s', seq: '%s')", hookPath, branch, uuidStr, seqStr)
	}

	if err := hooks.Run(ctx, runReq); err != nil {
		return InfoResult{}, err
	}

	return InfoResult{
		HookFound:    true,
		HookExecuted: true,
		HookPath:     hookPath,
		Env:          hookEnv,
	}, nil
}

func (a *App) newInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Execute .git-drev-info-hook to display project revision metadata",
		Long: `Executes the repository's .git-drev-info-hook script in an isolated subprocess
with injected GIT_DREV_* environment variables describing the active revision and Git context.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return errcode.NewError(errcode.ErrUsage, "info takes no arguments (got: %s)", args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			timeout, err := hook.ResolveTimeout(os.LookupEnv)
			if err != nil {
				return err
			}
			opts := InfoOptions{
				Quiet:       a.Opts.Quiet,
				Verbose:     a.Opts.Verbose,
				HookTimeout: timeout,
				CallerDir:   a.WorkingDir,
			}
			_, err = RunInfo(
				cmd.Context(),
				a.Inspector,
				a.Hooks,
				nil,
				a.Logger,
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
				opts,
			)
			return err
		},
	}

	return cmd
}
