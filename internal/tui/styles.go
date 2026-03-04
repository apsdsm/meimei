package tui

import "github.com/charmbracelet/lipgloss"

var (
	ColWidth = 22

	HeaderStyle   = lipgloss.NewStyle().Bold(true)
	DimStyle      = lipgloss.NewStyle().Faint(true)
	CursorStyle   = lipgloss.NewStyle().Reverse(true)
	SelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	ErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // red
	SuccessStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	SpinnerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan

	BorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	TableCellStyle = lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
	TableHeaderCellStyle = lipgloss.NewStyle().
				PaddingLeft(1).
				PaddingRight(1).
				Bold(true).
				Foreground(lipgloss.Color("4"))
	TableCursorStyle = lipgloss.NewStyle().
				PaddingLeft(1).
				PaddingRight(1).
				Background(lipgloss.Color("236")).
				Foreground(lipgloss.Color("15")).
				Bold(true)
)
