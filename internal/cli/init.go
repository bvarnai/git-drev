package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bvarnai/git-drev/internal/checks"
	"github.com/bvarnai/git-drev/internal/config"
	"github.com/bvarnai/git-drev/internal/domain"
	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/storage"
	"github.com/bvarnai/git-drev/internal/term"
	"github.com/spf13/cobra"
)

// InitOptions holds validated inputs for `git-drev init`.
type InitOptions struct {
	Host      string
	Path      string
	WorkDir   string
	Branch    string
	Yes       bool
	Force     bool
	Commit    bool
	NoCommit  bool
	SkipCheck bool
	CallerDir string
}

// RunInit executes the `git-drev init` workflow.
func RunInit(
	ctx context.Context,
	insp *git.Inspector,
	ops *git.Operations,
	storageMgr storage.Manager,
	logger *term.Logger,
	opts InitOptions,
) error {
	if err := ctx.Err(); err != nil {
		return errcode.WrapError(errcode.ErrInterrupted, err, "Operation interrupted")
	}

	if opts.Commit && opts.NoCommit {
		return errcode.NewError(errcode.ErrUsage, "Flags --commit and --no-commit cannot be used together")
	}

	startDir := opts.CallerDir
	if startDir == "" {
		startDir = "."
	}
	absStartDir, absErr := filepath.Abs(startDir)
	if absErr != nil {
		absStartDir = startDir
	}

	// 1. Resolve Git repository or prepare to initialize a fresh one
	needsGitInit := false
	repoRoot, _, err := checks.InsideWorkTree(ctx, insp, startDir)
	if err != nil {
		if !errors.Is(err, errcode.ErrNotRepository) {
			return err
		}

		// Outside a Git repository
		if !opts.Yes {
			if logger == nil {
				return errcode.NewError(errcode.ErrNotRepository, "Current directory is not a Git repository")
			}
			confirmed, promptErr := logger.Confirm("Current directory is not a Git repository. Initialize a new dedicated git-drev metadata repository with 'git init'? [Y/n]")
			if promptErr != nil {
				return promptErr
			}
			if !confirmed {
				return errcode.NewError(errcode.ErrNotRepository, "Current directory is not a Git repository").
					WithHint("Run inside a dedicated Git repository or confirm initialization.")
			}
		}
		needsGitInit = true
	} else {
		// Inside an existing Git repository: perform defensive source-repository check
		hasCommits, _ := insp.HasCommits(ctx, repoRoot)
		hasTracked, _ := insp.HasTrackedFiles(ctx, repoRoot)
		if hasCommits || hasTracked {
			if logger != nil {
				logger.Warn("This repository already contains commits or tracked files.")
				logger.Warn("git-drev manages a DEDICATED metadata repository (not an add-on to a source code repository).")
			}
			if !opts.Force {
				if opts.Yes {
					return errcode.NewError(errcode.ErrGitState, "Repository already contains commits or tracked files").
						WithHint("git-drev requires a dedicated metadata repository. Pass --force to override.")
				}
				if logger == nil {
					return errcode.NewError(errcode.ErrGitState, "Repository already contains commits or tracked files")
				}
				confirmed, promptErr := logger.Confirm("Continue initializing git-drev in this existing repository? [y/N]")
				if promptErr != nil {
					return promptErr
				}
				if !confirmed {
					return errcode.NewError(errcode.ErrGitState, "Initialization aborted by user")
				}
			}
		}
	}

	// 2. Check for existing repository configuration
	if !needsGitInit {
		existingConfig := config.FindRepoConfigFile(repoRoot)
		if existingConfig != "" && !opts.Force {
			if opts.Yes {
				return errcode.NewError(errcode.ErrConfig, "Configuration file already exists: '%s'", existingConfig).
					WithHint("Pass --force to overwrite the existing configuration.")
			}
			if logger == nil {
				return errcode.NewError(errcode.ErrConfig, "Configuration file already exists: '%s'", existingConfig)
			}
			overwrite, promptErr := logger.Confirm("Configuration file already exists. Overwrite? [y/N]")
			if promptErr != nil {
				return promptErr
			}
			if !overwrite {
				return errcode.NewError(errcode.ErrConfig, "Initialization aborted by user")
			}
		}
	}

	// 3. Collect configuration parameters
	globalDefaults := config.LoadGlobalDefaults()

	// Host
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		defaultHost := strings.TrimSpace(globalDefaults.Remote.Host)
		if opts.Yes && defaultHost != "" {
			host = defaultHost
		} else if !opts.Yes && logger != nil {
			var promptErr error
			host, promptErr = logger.Prompt("Remote SSH host", defaultHost)
			if promptErr != nil {
				return promptErr
			}
		} else {
			return errcode.NewError(errcode.ErrUsage, "Missing required flag --host in non-interactive mode")
		}
	}

	// Path
	rawPath := strings.TrimSpace(opts.Path)
	if rawPath == "" {
		defaultPath := strings.TrimSpace(globalDefaults.Remote.Path)
		if defaultPath == "" {
			defaultPath = "/srv/drev/{repo}"
		}
		if opts.Yes {
			rawPath = defaultPath
		} else if logger != nil {
			var promptErr error
			rawPath, promptErr = logger.Prompt("Remote storage path", defaultPath)
			if promptErr != nil {
				return promptErr
			}
		}
	}

	// WorkDir
	workDir := strings.TrimSpace(opts.WorkDir)
	if workDir == "" {
		defaultWorkDir := strings.TrimSpace(globalDefaults.WorkDir)
		if defaultWorkDir == "" {
			defaultWorkDir = config.DefaultWorkDir
		}
		if opts.Yes {
			workDir = defaultWorkDir
		} else if logger != nil {
			var promptErr error
			workDir, promptErr = logger.Prompt("Local data working directory", defaultWorkDir)
			if promptErr != nil {
				return promptErr
			}
		}
	}

	// Branch
	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		defaultBranch := strings.TrimSpace(globalDefaults.Branch.Default)
		if defaultBranch == "" {
			defaultBranch = config.DefaultBranchName
		}
		if opts.Yes {
			branch = defaultBranch
		} else if logger != nil {
			var promptErr error
			branch, promptErr = logger.Prompt("Default revision branch name", defaultBranch)
			if promptErr != nil {
				return promptErr
			}
		}
	}

	// Perform deferred git init or update unborn branch
	if needsGitInit {
		if err := ops.Init(ctx, absStartDir, branch); err != nil {
			return err
		}
		if logger != nil {
			logger.Infof("Initialized empty Git repository in '%s' (branch: '%s')", absStartDir, branch)
		}
		recheckRoot, _, recheckErr := checks.InsideWorkTree(ctx, insp, absStartDir)
		if recheckErr != nil {
			return recheckErr
		}
		repoRoot = recheckRoot
	} else if insp != nil && ops != nil {
		hasCommits, _ := insp.HasCommits(ctx, repoRoot)
		if !hasCommits {
			_ = ops.SetHeadBranch(ctx, repoRoot, branch)
		}
	}

	repoName := config.DetectRepoName(repoRoot)
	expandedPath := strings.ReplaceAll(rawPath, "{repo}", repoName)
	expandedPath = strings.ReplaceAll(expandedPath, "{project}", repoName)

	projectCfg := &domain.ProjectConfig{
		Remote: domain.RemoteConfig{
			Host: host,
			Path: expandedPath,
		},
		WorkDir: workDir,
		Branch: domain.BranchConfig{
			Default: branch,
		},
	}

	if err := config.NormalizeAndValidate(projectCfg); err != nil {
		return err
	}

	// 4. Remote access verification
	if !opts.SkipCheck && storageMgr != nil {
		if logger != nil {
			logger.Infof("Verifying remote storage access on '%s:%s'...", projectCfg.Remote.Host, projectCfg.Remote.Path)
		}
		access, checkErr := storageMgr.CheckAccess(ctx, projectCfg.Remote.Host, projectCfg.Remote.Path)
		if checkErr != nil {
			return checkErr
		}

		if !access.Exists {
			if !opts.Yes {
				if logger == nil {
					return errcode.NewError(errcode.ErrRemoteNotFound, "Remote directory '%s' does not exist on host '%s'", projectCfg.Remote.Path, projectCfg.Remote.Host)
				}
				createConfirmed, promptErr := logger.Confirm(fmt.Sprintf("Remote directory '%s' does not exist on host '%s'. Create it? [Y/n]", projectCfg.Remote.Path, projectCfg.Remote.Host))
				if promptErr != nil {
					return promptErr
				}
				if !createConfirmed {
					return errcode.NewError(errcode.ErrRemoteNotFound, "Remote directory '%s' does not exist on host '%s'", projectCfg.Remote.Path, projectCfg.Remote.Host)
				}
			}
			if mkErr := storageMgr.CreateDirectory(ctx, projectCfg.Remote.Host, projectCfg.Remote.Path); mkErr != nil {
				return mkErr
			}
			if logger != nil {
				logger.Infof("Created remote directory '%s' on host '%s'", projectCfg.Remote.Path, projectCfg.Remote.Host)
			}
			recheck, recheckErr := storageMgr.CheckAccess(ctx, projectCfg.Remote.Host, projectCfg.Remote.Path)
			if recheckErr != nil {
				return recheckErr
			}
			access = recheck
		}

		if logger != nil {
			if access.Writable {
				logger.Info("Remote storage verified (read-write access)")
			} else {
				logger.Warn("Remote storage path is read-only ('get' is available, 'put' requires write access)")
			}
		}
	}

	// 5. Generate .git-drev.yaml
	configToWrite := &domain.ProjectConfig{
		Remote: domain.RemoteConfig{
			Host: projectCfg.Remote.Host,
			Path: rawPath, // Preserve {repo} placeholder in saved file if provided by user
		},
		WorkDir: projectCfg.WorkDir,
		Branch: domain.BranchConfig{
			Default: projectCfg.Branch.Default,
		},
	}
	configContent := config.FormatRepoConfig(configToWrite)
	configPath := filepath.Join(repoRoot, ".git-drev.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		return errcode.WrapError(errcode.ErrRuntime, err, "Failed to write '%s'", configPath)
	}
	if logger != nil {
		logger.Info("Wrote configuration to '.git-drev.yaml'")
	}

	// 6. Update .gitignore
	if err := ensureGitIgnore(repoRoot, projectCfg.WorkDir); err != nil {
		return err
	}
	if logger != nil {
		logger.Infof("Configured '.gitignore' to ignore '/%s'", strings.Trim(filepath.ToSlash(projectCfg.WorkDir), "/"))
	}

	// 7. Create local workdir directory
	localWorkDirAbs := filepath.Join(repoRoot, projectCfg.WorkDir)
	if err := os.MkdirAll(localWorkDirAbs, 0o755); err == nil && logger != nil {
		logger.Infof("Created directory '%s'", projectCfg.WorkDir)
	}

	// 8. Initial commit
	shouldCommit := false
	if opts.NoCommit {
		shouldCommit = false
	} else if opts.Commit || opts.Yes {
		shouldCommit = true
	} else if logger != nil {
		confirmed, promptErr := logger.Confirm("Create commit for '.git-drev.yaml' and '.gitignore'? [Y/n]")
		if promptErr == nil && confirmed {
			shouldCommit = true
		}
	}

	if shouldCommit {
		if err := ops.StageFiles(ctx, repoRoot, ".git-drev.yaml", ".gitignore"); err != nil {
			return err
		}
		hasStaged := true
		if insp != nil {
			staged, stagedErr := insp.HasStagedChanges(ctx, repoRoot)
			if stagedErr != nil {
				return stagedErr
			}
			hasStaged = staged
		}
		if hasStaged {
			if err := ops.Commit(ctx, repoRoot, "chore: initialize git-drev configuration"); err != nil {
				return err
			}
			if logger != nil {
				logger.Info("Created initial commit for git-drev configuration")
			}
		} else if logger != nil {
			logger.Debug("No staged changes to commit (working tree clean)")
		}
	}

	// 9. Hint for Git remote if not configured
	if insp != nil {
		if _, remoteErr := insp.Runner().Run(ctx, repoRoot, "remote", "get-url", "origin"); remoteErr != nil && logger != nil {
			logger.Hint("Add a Git remote when ready: git remote add origin <url>")
		}
	}

	if logger != nil {
		logger.Info("git-drev repository initialization complete")
	}

	return nil
}

func ensureGitIgnore(repoRoot, workDir string) error {
	ignorePath := filepath.Join(repoRoot, ".gitignore")
	var existingContent string
	data, err := os.ReadFile(ignorePath)
	if err == nil {
		existingContent = string(data)
	} else if !os.IsNotExist(err) {
		return errcode.WrapError(errcode.ErrRuntime, err, "Failed to read '%s'", ignorePath)
	}

	normalizedTarget := strings.Trim(filepath.ToSlash(workDir), "/")
	lines := strings.Split(existingContent, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		cleanLine := strings.Trim(strings.TrimPrefix(trimmed, "/"), "/")
		if cleanLine == normalizedTarget {
			return nil // Already ignored
		}
	}

	var sb strings.Builder
	if len(existingContent) > 0 {
		sb.WriteString(existingContent)
		if !strings.HasSuffix(existingContent, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("# git-drev working directory\n")
	sb.WriteString("/" + normalizedTarget + "\n")

	if err := os.WriteFile(ignorePath, []byte(sb.String()), 0o644); err != nil {
		return errcode.WrapError(errcode.ErrRuntime, err, "Failed to write '%s'", ignorePath)
	}
	return nil
}

func (a *App) newInitCmd() *cobra.Command {
	opts := InitOptions{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a dedicated git-drev metadata repository",
		Long: `Initializes a dedicated git-drev metadata repository in the current directory.

Prompts for mandatory remote configuration (SSH host, remote path, and local workdir),
verifies remote SSH read/write access permissions, writes .git-drev.yaml, updates
.gitignore, and optionally creates the initial configuration commit.

Note: git-drev operates on a dedicated metadata repository where commits represent
revisions and branches represent data streams. It is not designed to be used inside
a regular source code repository.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.CallerDir = a.WorkingDir
			return RunInit(cmd.Context(), a.Inspector, a.Operations, a.Storage, a.Logger, opts)
		},
	}

	cmd.Flags().StringVar(&opts.Host, "host", "", "Remote SSH host (e.g. storage.example.com or user@host)")
	cmd.Flags().StringVar(&opts.Path, "path", "", "Remote storage base path (e.g. /srv/drev/{repo})")
	cmd.Flags().StringVar(&opts.WorkDir, "workdir", "", "Local working directory for tracked revisions (default: data)")
	cmd.Flags().StringVar(&opts.Branch, "branch", "", "Default baseline branch name (default: main)")
	cmd.Flags().BoolVarP(&opts.Yes, "yes", "y", false, "Accept default options non-interactively")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "Force overwrite of existing configuration or initialization inside non-empty repository")
	cmd.Flags().BoolVar(&opts.Commit, "commit", false, "Create commit for generated configuration and .gitignore")
	cmd.Flags().BoolVar(&opts.NoCommit, "no-commit", false, "Skip committing generated configuration and .gitignore")
	cmd.Flags().BoolVar(&opts.SkipCheck, "skip-check", false, "Skip remote SSH connection and access verification")

	return cmd
}
