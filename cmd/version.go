package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is what `meimei version` prints, and it tracks the git tag rather
// than leading it: the installer resolves `@latest` from tags, so a const ahead
// of the tag describes a build nobody can install.
//
// 1.x, not 2.x, although the rewrite is called v2 everywhere else. A module
// whose path has no /v2 suffix is one Go ignores v2+ tags for, so a v2.0.0 tag
// would leave `go install github.com/apsdsm/meimei@latest` on v0.1.0 — and
// adding the suffix to a CLI nobody imports would be ceremony with a cost. The
// v2 name stays what it has always been: the name of the rewrite.
const Version = "1.2.0"

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
