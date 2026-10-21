package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bvarnai/git-drev/internal/checks"
	"github.com/bvarnai/git-drev/internal/config"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/bvarnai/git-drev/internal/term"
	"github.com/spf13/cobra"
)

// GetOptions holds options for `git-drev get`.
type GetOptions struct {
	TargetDir string
	Latest    bool
	Stats     bool
	Verbose   bool
	Quiet     bool
	CallerDir string
}

// GetResult captures the outcome of `git-drev get`.
type GetResult struct {
	UUID          string
	TargetDir     string
	UpdatedBranch string
}

// RunGet executes the `git-drev get` workflow.
func RunGet(
	ctx context.Context,
	insp *git.Inspector,
	ops *git.Operations,
	stor storage.Manager,
	cfg *domain.ProjectConfig,
	logger *term.Logger,
	opts GetOptions,
) (*GetResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	timer := term.StartTimer()

	repoRoot, prefix, err := checks.InsideWorkTree(ctx, insp, opts.CallerDir)
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

	if err := checks.RemoteConfigured(cfg); err != nil {
		return nil, err
	}

	// 1. Resolve local target directory
	targetDir := opts.TargetDir
	if targetDir == "" {
		targetDir = filepath.Join(repoRoot, cfg.WorkDir)
	} else {
		if !filepath.IsAbs(targetDir) {
			callerDir := filepath.Join(repoRoot, prefix)
			targetDir = filepath.Clean(filepath.Join(callerDir, targetDir))
		}
	}

	// 2. Handle --latest if requested
	var updatedBranch string
	if opts.Latest {
		branch, detached, branchErr := insp.CurrentBranch(ctx, repoRoot)
		if branchErr != nil {
			return nil, branchErr
		}
		if detached {
			return nil, errcode.NewError(errcode.ErrDetachedHead, "Cannot update detached HEAD to latest").
				WithHint(`Use "git-drev select <branch>" to select a branch first.`)
		}

		if err := ops.EnsureRemoteFetchConfig(ctx, repoRoot); err != nil {
			return nil, err
		}

		if logger != nil {
			logger.Infof("Updating branch '%s' to 'origin/%s' before getting revision", branch, branch)
		}

		if err := ops.FetchAll(ctx, repoRoot, opts.Quiet && !opts.Verbose); err != nil {
			return nil, err
		}

		exists, err := insp.RemoteTrackingBranchExists(ctx, repoRoot, branch)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, errcode.NewError(errcode.ErrGitState, "Remote branch 'origin/%s' does not exist", branch)
		}

		upstreamRef := "refs/remotes/origin/" + branch
		if err := ops.ResetHard(ctx, repoRoot, upstreamRef); err != nil {
			return nil, err
		}
		updatedBranch = branch
	}

	// 3. Resolve UUID from HEAD commit metadata
	meta, headErr := insp.HeadCommitMeta(ctx, repoRoot)
	if headErr != nil || meta.UUID == "" || meta.UUID == domain.ZeroUUID || meta.IsMarker() {
		return nil, errcode.NewError(errcode.ErrGitState, "No revision committed yet on this branch").
			WithHint(`Use "git-drev put" to create the initial revision.`)
	}
	uuid := meta.UUID

	// 4. Verify remote directory exists
	var checkSpinner *term.Spinner
	if logger != nil && logger.IsTTY() {
		checkSpinner = logger.StartSpinner(fmt.Sprintf("Connecting to host '%s'...", cfg.Remote.Host))
	}
	exists, checkErr := stor.HasRevision(ctx, cfg.Remote.Host, cfg.Remote.Path, uuid)
	if checkSpinner != nil {
		checkSpinner.Stop()
	}
	if checkErr != nil {
		return nil, checkErr
	}
	if !exists {
		return nil, errcode.NewError(errcode.ErrRemoteNotFound, "Unable to find revision '%s' on host '%s'", uuid, cfg.Remote.Host).
			WithHint("Revision might be gone already (that's normal depending on your retention policy).")
	}

	// 5. Prepare local target directory
	info, statErr := os.Stat(targetDir)
	if statErr == nil {
		if !info.IsDir() {
			return nil, errcode.NewError(errcode.ErrRuntime, "Target path '%s' exists and is not a directory", targetDir)
		}
	} else if errors.Is(statErr, fs.ErrNotExist) {
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return nil, errcode.WrapError(errcode.ErrRuntime, err, "Failed to create target directory '%s'", targetDir)
		}
	} else {
		return nil, errcode.WrapError(errcode.ErrRuntime, statErr, "Failed to access target directory '%s'", targetDir)
	}

	// 6. Download payload via rsync
	shortUUID := uuid
	if len(shortUUID) > 8 {
		shortUUID = shortUUID[:8]
	}
	var getSpinner *term.Spinner
	if !opts.Verbose && !opts.Stats && logger != nil {
		getSpinner = logger.StartSpinner(fmt.Sprintf("Downloading directory revision '%s' from host '%s'...", shortUUID, cfg.Remote.Host))
		defer getSpinner.Stop()
	} else if logger != nil {
		logger.Infof("Downloading directory revision '%s' from host '%s'...", shortUUID, cfg.Remote.Host)
	}

	syncReq := storage.TransferRequest{
		Host:        cfg.Remote.Host,
		BasePath:    cfg.Remote.Path,
		UUID:        uuid,
		LocalDir:    targetDir,
		Verbose:     opts.Verbose,
		Quiet:       opts.Quiet,
		Stats:       opts.Stats,
		UserOptions: cfg.Remote.Rsync.Get,
	}

	if logger != nil {
		logger.Debugf("Initiating rsync download from %s:%s/%s to %s", cfg.Remote.Host, cfg.Remote.Path, uuid, targetDir)
	}

	if err := stor.Download(ctx, syncReq); err != nil {
		return nil, err
	}
	if getSpinner != nil {
		getSpinner.Stop()
	}
	if logger != nil {
		logger.Infof("Retrieved directory revision '%s' from host '%s'", shortUUID, cfg.Remote.Host)
	}

	if logger != nil {
		logger.Info(timer.String())
	}

	return &GetResult{
		UUID:          uuid,
		TargetDir:     targetDir,
		UpdatedBranch: updatedBranch,
	}, nil
}

func (a *App) newGetCmd() *cobra.Command {
	var latest bool
	var stats bool

	cmd := &cobra.Command{
		Use:   "get [flags] [<target_directory>]",
		Short: "Synchronize local directory with the remote revision referenced by HEAD",
		Long: `Downloads the remote directory revision corresponding to HEAD's UUID via rsync
into the configured workdir (or specified target directory).`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return errcode.NewError(errcode.ErrUsage, "Too many arguments: get takes at most 1 argument (<target_directory>), got %d", len(args))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			target := ""
			if len(args) == 1 {
				target = args[0]
			}
			opts := GetOptions{
				TargetDir: target,
				Latest:    latest,
				Stats:     stats,
				Verbose:   a.Opts.Verbose,
				Quiet:     a.Opts.Quiet,
				CallerDir: a.WorkingDir,
			}
			_, err := RunGet(
				cmd.Context(),
				a.Inspector,
				a.Operations,
				a.Storage,
				nil,
				a.Logger,
				opts,
			)
			return err
		},
	}

	cmd.Flags().BoolVarP(&latest, "latest", "l", false, "Update current branch to origin before getting revision")
	cmd.Flags().BoolVar(&stats, "stats", false, "Print verbose rsync transfer statistics")

	return cmd
}
