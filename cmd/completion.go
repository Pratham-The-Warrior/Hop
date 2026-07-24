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

	// --- Dynamic completion functions ---

	// 'hop http <port>' suggests common development ports
	httpCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return []string{
			"3000\tNode.js / React dev server",
			"8080\tCommon HTTP alternative",
			"8000\tPython / Django dev server",
			"5000\tFlask dev server",
			"5173\tVite dev server",
			"4200\tAngular dev server",
			"3001\tNext.js alternate",
			"8888\tJupyter notebook",
		}, cobra.ShellCompDirectiveNoFileComp
	}

	// 'hop share <file>' provides file/directory completion (default shell behavior)
	shareCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		// ShellCompDirectiveDefault enables default file completion
		return nil, cobra.ShellCompDirectiveDefault
	}

	// 'hop get <token>' disables file completion since tokens are typed manually
	getCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Flag completions for common bandwidth limit values
	shareCmd.RegisterFlagCompletionFunc("limit", completeBandwidthLimit)
	getCmd.RegisterFlagCompletionFunc("limit", completeBandwidthLimit)

	// Flag completions for output directory on 'hop get'
	getCmd.RegisterFlagCompletionFunc("output", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterDirs
	})
}

// completeBandwidthLimit suggests common bandwidth limit values for --limit flag.
func completeBandwidthLimit(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return []string{
		"1MB/s\tSlow — preserve bandwidth",
		"5MB/s\tModerate",
		"10MB/s\tFast",
		"25MB/s\tVery fast",
		"50MB/s\tMaximum practical",
	}, cobra.ShellCompDirectiveNoFileComp
}
