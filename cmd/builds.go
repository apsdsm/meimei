package cmd

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	meimeiaws "github.com/apsdsm/meimei/internal/aws"
	"github.com/apsdsm/meimei/internal/tui"
	"github.com/apsdsm/meimei/internal/types"
	"github.com/spf13/cobra"
)

var buildsCmd = &cobra.Command{
	Use:   "builds",
	Short: "List available builds",
	RunE:  runBuilds,
}

func init() {
	buildsCmd.Flags().Int("limit", 20, "Maximum number of builds to show")
	buildsCmd.Flags().Bool("mine", false, "Only show builds made by you")
	buildsCmd.Flags().String("by", "", "Only show builds made by NAME")
	buildsCmd.Flags().String("filter", "", "Only show builds whose name contains PATTERN")
	rootCmd.AddCommand(buildsCmd)
}

func runBuilds(cmd *cobra.Command, args []string) error {
	filter, err := buildFilterFromFlags(cmd)
	if err != nil {
		return err
	}

	ctx := context.Background()
	client, err := meimeiaws.NewClient(ctx, projectCfg)
	if err != nil {
		return fmt.Errorf("initializing AWS: %w", err)
	}

	builds, err := client.QueryBuilds(ctx, client.Account.BuildsTable, client.Account.CodeDeployAppName, filter)
	if err != nil {
		return fmt.Errorf("querying builds: %w", err)
	}

	model := tui.NewBuildsListModel(builds, projectCfg.ExtraColumns)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

func buildFilterFromFlags(cmd *cobra.Command) (types.BuildFilter, error) {
	limit, _ := cmd.Flags().GetInt("limit")
	mine, _ := cmd.Flags().GetBool("mine")
	by, _ := cmd.Flags().GetString("by")
	filterName, _ := cmd.Flags().GetString("filter")

	filterBy := by
	if mine {
		user := os.Getenv("USER")
		if user == "" {
			return types.BuildFilter{}, fmt.Errorf("$USER not set")
		}
		filterBy = user
	}

	return types.BuildFilter{
		Limit:      limit,
		FilterBy:   filterBy,
		FilterName: filterName,
	}, nil
}
