package cmd

import (
	"github.com/apsdsm/meimei/internal/config"
	"github.com/spf13/cobra"
)

var cfgPath string

var rootCmd = &cobra.Command{
	Use:   "meimei",
	Short: "Build and deploy this project's images",
	// Errors are already reported by Execute; cobra printing usage on top of a
	// runtime failure buries the message that matters.
	SilenceUsage: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "",
		"path to "+config.FileName+" (default: search up from the working directory)")
}

// loadConfig resolves the config for a command. Loading is per-command rather
// than in a PersistentPreRun so that commands which need no config — version,
// and anything that scaffolds one — do not have to opt out of it.
func loadConfig() (*config.Config, error) {
	if cfgPath != "" {
		return config.LoadFrom(cfgPath)
	}
	return config.Load()
}

func Execute() error {
	return rootCmd.Execute()
}
