package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bvarnai/git-drev/internal/checks"
	"github.com/bvarnai/git-drev/internal/config"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/hook"
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/bvarnai/git-drev/internal/term"
	"github.com/bvarnai/git-drev/internal/version"
	"github.com/spf13/cobra"
)

// MaxPushRetries defines the maximum optimistic concurrency push attempts.
const MaxPushRetries = 5

// PutOptions encapsulates inputs for `git-drev put`.
type PutOptions struct {
	SourceDir       string
	Sequence        string
	NoSequenceCheck bool
	Stats           bool
	Verbose         bool
	Quiet           bool
	RawArgs         []string
	CallerDir       string
}

// PutResult captures the outcome of `git-drev put`.
type PutResult struct {
	UUID         string
	Seq          string
	Branch       string
	DroppedOlder bool
}

// RunPut executes the `git-drev put` workflow.
func RunPut(
	ctx context.Context,
	insp *git.Inspector,
	ops *git.Operations,
	stor storage.Manager,
	hooks hook.Runner,
	cfg *domain.ProjectConfig,
	logger *term.Logger,
	stdout, stderr io.Writer,
	opts PutOptions,
) (*PutResult, error) {
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

	// 1. Verify attached HEAD
	branch, err := checks.AttachedBranch(ctx, insp, repoRoot)
	if err != nil {
		return nil, err
	}

	// 2. Check for index.lock
	if err := checks.NoIndexLock(repoRoot); err != nil {
		return nil, err
	}

	// 3. Resolve and validate source directory
	sourceDir := opts.SourceDir
	if sourceDir == "" {
		sourceDir = filepath.Join(repoRoot, cfg.WorkDir)
	} else {
		if !filepath.IsAbs(sourceDir) {
			callerDir := filepath.Join(repoRoot, prefix)
			sourceDir = filepath.Clean(filepath.Join(callerDir, sourceDir))
		}
	}

	fi, statErr := os.Stat(sourceDir)
	if statErr != nil || !fi.IsDir() {
		return nil, errcode.NewError(errcode.ErrUsage, "Source directory '%s' does not exist or is not a directory", sourceDir)
	}

	// 4. Validate or default sequence format
	if opts.Sequence == "" {
		opts.Sequence = strconv.FormatInt(time.Now().Unix(), 10)
	} else {
		if _, err := strconv.ParseUint(opts.Sequence, 10, 64); err != nil {
			return nil, errcode.NewError(errcode.ErrUsage, "Invalid sequence %q: must be a non-negative integer", opts.Sequence)
		}
	}

	// 5. Generate new UUID
	newUUID, err := domain.NewUUID()
	if err != nil {
		return nil, errcode.WrapError(errcode.ErrRuntime, err, "Failed to generate UUID")
	}

	// 6. Attempt server-side baseline clone
	if lastMeta, headErr := insp.HeadCommitMeta(ctx, repoRoot); headErr == nil &&
		domain.IsValidUUID(lastMeta.UUID) && lastMeta.UUID != domain.ZeroUUID {

		shortPrev := lastMeta.UUID
		if len(shortPrev) > 8 {
			shortPrev = shortPrev[:8]
		}
		var cloneSpinner *term.Spinner
		if logger != nil && logger.IsTTY() {
			cloneSpinner = logger.StartSpinner(fmt.Sprintf("Cloning baseline revision '%s' on host '%s'...", shortPrev, cfg.Remote.Host))
		}
		cloneErr := stor.CloneBaseline(ctx, cfg.Remote.Host, cfg.Remote.Path, lastMeta.UUID, newUUID)
		if cloneSpinner != nil {
			cloneSpinner.Stop()
		}
		if cloneErr != nil {
			if logger != nil {
				logger.Debugf("Baseline hardlink clone from '%s' to '%s' failed: %v", lastMeta.UUID, newUUID, cloneErr)
				logger.Info("Unable to use previous revision, performing a full copy")
			}
		} else if logger != nil {
			logger.Debugf("Cloned baseline revision '%s' to '%s' on '%s'", lastMeta.UUID, newUUID, cfg.Remote.Host)
		}
	} else {
		if logger != nil {
			logger.Debugf("No valid parent revision on HEAD (err=%v, uuid=%q); skipping baseline clone", headErr, lastMeta.UUID)
			logger.Info("Unable to use previous revision, performing a full copy")
		}
	}

	// Staged cleanup guarantee
	stagedRemote := true
	defer func() {
		if stagedRemote {
			if logger != nil {
				logger.Debugf("Cleaning up uncommitted remote revision directory '%s' on host '%s'", newUUID, cfg.Remote.Host)
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = stor.RemoveRevision(cleanupCtx, cfg.Remote.Host, cfg.Remote.Path, newUUID)
		}
	}()

	// 7. Upload directory payload via rsync
	shortNew := newUUID
	if len(shortNew) > 8 {
		shortNew = shortNew[:8]
	}
	var putSpinner *term.Spinner
	if !opts.Verbose && !opts.Stats && logger != nil {
		putSpinner = logger.StartSpinner(fmt.Sprintf("Uploading directory revision '%s' to host '%s'...", shortNew, cfg.Remote.Host))
		defer putSpinner.Stop()
	} else if logger != nil {
		logger.Infof("Uploading directory revision '%s' to host '%s'...", shortNew, cfg.Remote.Host)
	}

	syncReq := storage.TransferRequest{
		Host:        cfg.Remote.Host,
		BasePath:    cfg.Remote.Path,
		UUID:        newUUID,
		LocalDir:    sourceDir,
		Verbose:     opts.Verbose,
		Quiet:       opts.Quiet,
		Stats:       opts.Stats,
		UserOptions: cfg.Remote.Rsync.Put,
	}

	if logger != nil {
		logger.Debugf("Initiating rsync upload from %s to %s:%s/%s (user_options=%q)", sourceDir, cfg.Remote.Host, cfg.Remote.Path, newUUID, cfg.Remote.Rsync.Put)
	}

	if err := stor.Upload(ctx, syncReq); err != nil {
		return nil, err
	}
	if putSpinner != nil {
		putSpinner.Stop()
	}
	if logger != nil {
		logger.Infof("Uploaded directory revision '%s' to host '%s'", shortNew, cfg.Remote.Host)
	}

	// 8. Execute .git-drev-put-hook if present
	hookFile := hook.HookFileName(hook.KindPut)
	hookPath := filepath.Join(repoRoot, hookFile)
	if hfi, herr := os.Stat(hookPath); herr == nil && !hfi.IsDir() {
		hookTimeout, _ := hook.ResolveTimeout(os.LookupEnv)
		hookEnv := hook.Env{
			Version: version.String(),
			UUID:    newUUID,
			Seq:     opts.Sequence,
			Branch:  branch,
			WorkDir: filepath.Join(repoRoot, cfg.WorkDir),
			Root:    repoRoot,
		}

		runReq := hook.RunRequest{
			Kind:    hook.KindPut,
			Path:    hookPath,
			WorkDir: repoRoot,
			Args:    opts.RawArgs,
			Env:     hookEnv,
			Timeout: hookTimeout,
			Stdout:  stdout,
			Stderr:  stderr,
		}

		if logger != nil {
			logger.Debugf("Executing put hook '%s' (timeout: %s, uuid: '%s', seq: '%s')", hookPath, hookTimeout, newUUID, opts.Sequence)
		}

		if err := hooks.Run(ctx, runReq); err != nil {
			return nil, err
		}
	} else if logger != nil {
		logger.Debugf("No put hook found at '%s'; skipping", hookPath)
	}

	// 9. Stage revision metadata
	if logger != nil {
		logger.Info("Staging revision metadata")
	}
	if err := ops.StageMetadata(ctx, repoRoot, cfg.WorkDir); err != nil {
		return nil, err
	}

	// 10. Commit revision metadata
	if logger != nil {
		logger.Info("Committing revision metadata")
	}
	meta := domain.RevisionMeta{
		UUID: newUUID,
		Seq:  opts.Sequence,
	}
	commitJSON, err := meta.MarshalSubject()
	if err != nil {
		return nil, errcode.WrapError(errcode.ErrRuntime, err, "Failed to marshal revision metadata")
	}

	if err := ops.CommitAllowEmpty(ctx, repoRoot, commitJSON); err != nil {
		return nil, err
	}

	// 11. Optimistic concurrency push loop
	if logger != nil {
		logger.Infof("Pushing revision metadata to 'origin/%s'", branch)
	}

	for attempt := 1; attempt <= MaxPushRetries; attempt++ {
		stale, _, pushErr := ops.PushForceWithLease(ctx, repoRoot, branch, false)
		if pushErr == nil {
			// Succeeded! Disarm cleanup
			stagedRemote = false
			if logger != nil {
				logger.Info(timer.String())
			}
			return &PutResult{
				UUID:   newUUID,
				Seq:    opts.Sequence,
				Branch: branch,
			}, nil
		}

		if !stale {
			return nil, pushErr
		}

		if attempt == MaxPushRetries {
			return nil, errcode.NewError(errcode.ErrGitExec, "Exceeded maximum push retries (%d) due to concurrent updates on origin/%s", MaxPushRetries, branch)
		}

		if logger != nil {
			logger.Info("Stale info detected, trying to resolve")
		}

		if err := ops.FetchAll(ctx, repoRoot, opts.Quiet && !opts.Verbose); err != nil {
			return nil, err
		}

		// Sequence check
		if !opts.NoSequenceCheck {
			upstreamRef := "refs/remotes/origin/" + branch
			remoteMeta, metaErr := insp.BranchCommitMeta(ctx, repoRoot, upstreamRef)
			if metaErr == nil && remoteMeta.Seq != "" && opts.Sequence != "" {
				if logger != nil {
					logger.Debugf("Comparing sequences on 'origin/%s': local=%q, remote=%q", branch, opts.Sequence, remoteMeta.Seq)
				}
				remoteSeq, rErr := strconv.ParseUint(remoteMeta.Seq, 10, 64)
				localSeq, lErr := strconv.ParseUint(opts.Sequence, 10, 64)
				if rErr == nil && lErr == nil && localSeq < remoteSeq {
					if logger != nil {
						logger.Info("Newer revision found, dropping older (hard reset)")
					}
					// Disarm cleanup since we accept remote state
					stagedRemote = false
					if err := ops.ResetHard(ctx, repoRoot, upstreamRef); err != nil {
						return nil, err
					}
					if logger != nil {
						logger.Info(timer.String())
					}
					return &PutResult{
						DroppedOlder: true,
						Branch:       branch,
					}, nil
				}
			}
		}

		// Rebase local commit onto remote tip
		upstreamRef := "refs/remotes/origin/" + branch
		if logger != nil {
			logger.Debugf("Rebasing local revision commit onto '%s' (attempt %d/%d)", upstreamRef, attempt, MaxPushRetries)
		}
		if rebaseErr := ops.RebaseTheirs(ctx, repoRoot, upstreamRef); rebaseErr != nil {
			_ = ops.RebaseAbort(ctx, repoRoot)
			return nil, rebaseErr
		}

		// Backoff with exponential delay
		backoff := time.Duration(50*(1<<uint(attempt-1))) * time.Millisecond
		time.Sleep(backoff)
	}

	return nil, errcode.NewError(errcode.ErrGitExec, "Push failed after %d retries", MaxPushRetries)
}

func (a *App) newPutCmd() *cobra.Command {
	var sequence string
	var noSequenceCheck bool
	var stats bool

	cmd := &cobra.Command{
		Use:   "put [flags] [<source_directory>]",
		Short: "Upload a directory revision to remote storage and commit metadata",
		Long: `Synchronizes local directory contents to a new remote UUID revision directory via rsync,
executes .git-drev-put-hook (if present), records revision metadata in a Git commit,
and publishes the commit to origin with optimistic concurrency control.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return errcode.NewError(errcode.ErrUsage, "Too many arguments: put takes at most 1 argument (<source_directory>), got %d", len(args))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			source := ""
			if len(args) == 1 {
				source = args[0]
			}
			opts := PutOptions{
				SourceDir:       source,
				Sequence:        sequence,
				NoSequenceCheck: noSequenceCheck,
				Stats:           stats,
				Verbose:         a.Opts.Verbose,
				Quiet:           a.Opts.Quiet,
				RawArgs:         args,
				CallerDir:       a.WorkingDir,
			}
			_, err := RunPut(
				cmd.Context(),
				a.Inspector,
				a.Operations,
				a.Storage,
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

	cmd.Flags().StringVarP(&sequence, "sequence", "s", "", "Sequence number to associate with the revision")
	cmd.Flags().BoolVar(&noSequenceCheck, "no-sequence-check", false, "Bypass sequence comparison when resolving concurrent pushes")
	cmd.Flags().BoolVar(&stats, "stats", false, "Print verbose rsync transfer statistics")

	return cmd
}
