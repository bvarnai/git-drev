package cli

import (
	"context"
	"errors"
	"io"

	"github.com/bvarnai/git-drev/internal/errcode"
	"github.com/bvarnai/git-drev/internal/git"
	"github.com/bvarnai/git-drev/internal/hook"
	"github.com/bvarnai/git-drev/internal/term"
	"github.com/bvarnai/git-drev/internal/transport"
	"github.com/bvarnai/git-drev/internal/version"
	"github.com/spf13/cobra"
)

// GlobalOptions stores application-wide flag states.
type GlobalOptions struct {
	Verbose bool
	Quiet   bool
	NoColor bool
}

// App wires all dependencies and provides the root command dispatcher.
type App struct {
	WorkingDir string
	RootCmd    *cobra.Command
	Logger     *term.Logger
	Runner     git.Runner
	Inspector  *git.Inspector
	Operations *git.Operations
	Remote     transport.RemoteRunner
	Hooks      hook.Runner
	Opts       GlobalOptions
	Stdout     io.Writer
	Stderr     io.Writer
}

// NewApp initializes the Cobra command tree and wires global flags.
func NewApp(stdout, stderr io.Writer, runner git.Runner) *App {
	opts := GlobalOptions{}

	app := &App{
		Opts:   opts,
		Stdout: stdout,
		Stderr: stderr,
		Logger: term.New(term.Options{
			Stdout:      stdout,
			Stderr:      stderr,
			NoColorFlag: false,
			Quiet:       false,
			Verbose:     false,
			IsTTY:       term.IsStreamTTY(stderr),
		}),
	}

	if runner == nil {
		runner = git.NewExecRunner(func(gitCmd string) {
			if app.Logger != nil {
				app.Logger.Debugf("executing: %s", gitCmd)
			}
		})
	}
	app.Runner = runner
	app.Inspector = git.NewInspector(runner)
	app.Operations = git.NewOperations(runner)
	app.Remote = transport.NewExecRemoteRunner(stdout, stderr)
	app.Hooks = hook.NewExecRunner()

	rootCmd := &cobra.Command{
		Use:     "git-drev",
		Version: version.String(),
		Short:   "Git CLI extension for tracking directory revisions on remote storage",
		Long: `git-drev is a Git CLI extension that synchronizes and versions directory trees
on remote SSH/rsync storage hosts, binding them to Git commit metadata and branches.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if app.Opts.Verbose && app.Opts.Quiet {
				return errcode.NewError(errcode.ErrUsage, "flags --verbose and --quiet cannot be used together")
			}
			app.Logger = term.New(term.Options{
				Stdout:      stdout,
				Stderr:      stderr,
				NoColorFlag: app.Opts.NoColor,
				Quiet:       app.Opts.Quiet,
				Verbose:     app.Opts.Verbose,
				IsTTY:       term.IsStreamTTY(stderr),
			})
			return nil
		},
	}

	rootCmd.SetVersionTemplate("{{.Version}}\n")
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// Global persistent flags
	rootCmd.PersistentFlags().BoolVarP(&app.Opts.Verbose, "verbose", "v", false, "Enable verbose diagnostic output")
	rootCmd.PersistentFlags().BoolVarP(&app.Opts.Quiet, "quiet", "q", false, "Suppress informational notices and hints")
	rootCmd.PersistentFlags().BoolVar(&app.Opts.NoColor, "no-color", false, "Disable ANSI color styling")

	// Register subcommands
	rootCmd.AddCommand(app.newConfigCmd())
	rootCmd.AddCommand(app.newNameCmd())
	rootCmd.AddCommand(app.newCreateCmd())
	rootCmd.AddCommand(app.newSelectCmd())
	rootCmd.AddCommand(app.newUpdateCmd())
	rootCmd.AddCommand(app.newInfoCmd())
	rootCmd.AddCommand(app.newUsageCmd())
	rootCmd.AddCommand(app.newGetCmd())
	rootCmd.AddCommand(app.newPutCmd())
	rootCmd.AddCommand(app.newCompletionCmd())

	app.RootCmd = rootCmd
	return app
}

// Execute runs the Cobra command tree with the provided context.
func (a *App) Execute(ctx context.Context) error {
	return a.RootCmd.ExecuteContext(ctx)
}

// Run executes the command tree from main.go.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return RunInDir(ctx, "", args, stdout, stderr)
}

// RunInDir runs the application within a specific working directory.
func RunInDir(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) int {
	app := NewApp(stdout, stderr, nil)
	app.WorkingDir = dir
	app.RootCmd.SetArgs(args)

	err := app.Execute(ctx)
	if err == nil {
		return errcode.ExitSuccess
	}

	if app.Logger == nil {
		app.Logger = term.New(term.Options{
			Stdout:      stdout,
			Stderr:      stderr,
			NoColorFlag: app.Opts.NoColor,
			Quiet:       app.Opts.Quiet,
			Verbose:     app.Opts.Verbose,
			IsTTY:       term.IsStreamTTY(stderr),
		})
	}

	var appErr *errcode.AppError
	if errors.As(err, &appErr) {
		app.Logger.LogError(appErr)
		return errcode.ExitCode(appErr)
	}

	app.Logger.Errorf("%v", err)
	return errcode.ExitUsage
}
