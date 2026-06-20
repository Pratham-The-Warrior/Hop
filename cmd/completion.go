package cmd

import (
	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for hop.

To load completions:

Bash:
  $ source <(hop completion bash)
  # To load completions for each session, execute once:
  # Linux:
  $ hop completion bash > /etc/bash_completion.d/hop
  # macOS:
  $ hop completion bash > $(brew --prefix)/etc/bash_completion.d/hop

Zsh:
  $ source <(hop completion zsh)
  # To load completions for each session, execute once:
  $ hop completion zsh > "${fpath[1]}/_hop"

Fish:
  $ hop completion fish | source
  # To load completions for each session, execute once:
  $ hop completion fish > ~/.config/fish/completions/hop.fish

PowerShell:
  PS> hop completion powershell | Out-String | Invoke-Expression
  # To load completions for each session, execute once:
  PS> hop completion powershell > hop.ps1
  # and source this file from your PowerShell profile.
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.ExactValidArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		switch args[0] {
		case "bash":
			rootCmd.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			rootCmd.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			rootCmd.GenFishCompletion(cmd.OutOrStdout(), true)
		case "powershell":
			rootCmd.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
		}
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
