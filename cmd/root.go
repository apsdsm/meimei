package cmd

import (
	"github.com/apsdsm/meimei/internal/config"
	"github.com/spf13/cobra"
)

var (
	cfgPath   string
	projectCfg *config.ProjectConfig
)

var rootCmd = &cobra.Command{
	Use:   "meimei",
	Short: "Deployment and build management CLI",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for init command
		if cmd.Name() == "init" || cmd.Name() == "version" {
			return nil
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		projectCfg = cfg
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "", "path to .meimei.yaml (default: search up from cwd)")
}

func Execute() error {
	return rootCmd.Execute()
}
