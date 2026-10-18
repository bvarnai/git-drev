package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
)

// completeBranchNames provides dynamic shell autocompletion for branch names and tags.
func (a *App) completeBranchNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	workDir := "."
	if a.WorkingDir != "" {
		workDir = a.WorkingDir
	}

	if a.Inspector == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	branches, err := a.Inspector.ListAllBranchNames(context.Background(), workDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	tags, _ := a.Inspector.ListAllTags(context.Background(), workDir)

	var matches []string
	for _, b := range branches {
		if strings.HasPrefix(b, toComplete) {
			matches = append(matches, b)
		}
	}
	for _, t := range tags {
		if strings.HasPrefix(t, toComplete) {
			matches = append(matches, t)
		}
	}

	return matches, cobra.ShellCompDirectiveNoFileComp
}

// newCompletionCmd constructs the 'completion' command.
func (a *App) newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell autocompletion script",
		Long: `Generate shell autocompletion script for git-drev commands, flags, and branch names.

To load completions in your current shell session:

  Bash:
    source <(git-drev completion bash)

  Zsh:
    source <(git-drev completion zsh)

  Fish:
    git-drev completion fish | source

  PowerShell:
    git-drev completion powershell | Out-String | Invoke-Expression

To configure your shell to load completions automatically on startup, add the source
command to your ~/.bashrc, ~/.zshrc, or config.fish file.`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.ExactValidArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			}
			return nil
		},
	}

	return cmd
}
