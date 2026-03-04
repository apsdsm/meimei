package cmd

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	meimeiaws "github.com/apsdsm/meimei/internal/aws"
	"github.com/apsdsm/meimei/internal/tui"
	"github.com/spf13/cobra"
)

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy a build to target environments",
	RunE:  runDeploy,
}

func init() {
	deployCmd.Flags().Int("limit", 20, "Maximum number of builds to show")
	deployCmd.Flags().Bool("mine", false, "Only show builds made by you")
	deployCmd.Flags().String("by", "", "Only show builds made by NAME")
	deployCmd.Flags().String("filter", "", "Only show builds whose name contains PATTERN")
	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	filter, err := buildFilterFromFlags(cmd)
	if err != nil {
		return err
	}

	ctx := context.Background()
	client, err := meimeiaws.NewClient(ctx, projectCfg)
	if err != nil {
		return fmt.Errorf("initializing AWS: %w", err)
	}

	model := tui.NewDeployModel(client, filter, projectCfg.ExtraColumns)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}
