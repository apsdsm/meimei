package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/apsdsm/meimei/internal/config"
	"github.com/apsdsm/meimei/internal/types"
)

// RenderBuildsTable renders a lipgloss table of builds with a highlighted cursor row.
// offset and limit control the visible window (for scrolling).
// extraColumns defines project-specific columns to append from Build.Extra.
func RenderBuildsTable(builds []types.Build, cursor, offset, limit int, extraColumns []config.ExtraColumn) string {
	end := offset + limit
	if end > len(builds) {
		end = len(builds)
	}
	visible := builds[offset:end]

	// Core headers
	headers := []string{"#", "By", "At", "ID", "Name"}
	for _, col := range extraColumns {
		headers = append(headers, col.Header)
	}

	rows := make([][]string, len(visible))
	for i, b := range visible {
		row := []string{
			fmt.Sprintf("%d", offset+i+1),
			b.BuildBy,
			b.BuildAt,
			b.BuildID,
			b.Release,
		}
		for _, col := range extraColumns {
			row = append(row, b.Extra[col.Field])
		}
		rows[i] = row
	}

	cursorInView := cursor - offset

	t := table.New().
		Headers(headers...).
		Rows(rows...).
		Border(lipgloss.RoundedBorder()).
		BorderStyle(BorderStyle).
		BorderHeader(true).
		BorderColumn(true).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return TableHeaderCellStyle
			}
			if row == cursorInView {
				return TableCursorStyle
			}
			return TableCellStyle
		})

	return t.Render()
}
