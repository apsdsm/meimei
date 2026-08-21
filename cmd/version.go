package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is pre-release while v2 is being built: the command surface is still
// moving, and no tag is cut until build and deploy have landed. Kept distinct
// from the 0.1.0 installed at /usr/local/bin so `meimei version` says at a
// glance which one you are running.
const Version = "2.0.0-dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("meimei", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
