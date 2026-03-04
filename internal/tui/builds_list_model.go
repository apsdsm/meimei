package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/apsdsm/meimei/internal/config"
	"github.com/apsdsm/meimei/internal/types"
)

type BuildsListModel struct {
	allBuilds    []types.Build // full set from DynamoDB
	filtered     []types.Build // after applying filter
	cursor       int
	offset       int
	height       int
	extraColumns []config.ExtraColumn

	filtering bool   // whether the filter input is active
	filter    string // current filter text
}

func NewBuildsListModel(builds []types.Build, extraColumns []config.ExtraColumn) BuildsListModel {
	return BuildsListModel{
		allBuilds:    builds,
		filtered:     builds,
		height:       20,
		extraColumns: extraColumns,
	}
}

func (m BuildsListModel) Init() tea.Cmd {
	return nil
}

func (m BuildsListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height - 10 // title + table chrome + filter + help
		if m.height < 3 {
			m.height = 3
		}

	case tea.KeyMsg:
		if m.filtering {
			switch msg.String() {
			case "enter", "esc":
				m.filtering = false
			case "backspace":
				if len(m.filter) > 0 {
					m.filter = m.filter[:len(m.filter)-1]
					m.applyFilter()
				}
			case "ctrl+c":
				return m, tea.Quit
			default:
				if len(msg.String()) == 1 {
					m.filter += msg.String()
					m.applyFilter()
				}
			}
			return m, nil
		}

		// Normal mode
		switch msg.String() {
		case "/":
			m.filtering = true
		case "esc":
			if m.filter != "" {
				m.filter = ""
				m.applyFilter()
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				if m.cursor < m.offset {
					m.offset = m.cursor
				}
			}
		case "down", "j":
			if m.cursor < len(m.filtered)-1 {
				m.cursor++
				if m.cursor >= m.offset+m.height {
					m.offset = m.cursor - m.height + 1
				}
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *BuildsListModel) applyFilter() {
	if m.filter == "" {
		m.filtered = m.allBuilds
	} else {
		needle := strings.ToLower(m.filter)
		var out []types.Build
		for _, b := range m.allBuilds {
			haystack := strings.ToLower(b.BuildBy + " " + b.BuildAt + " " + b.BuildID + " " + b.Release)
			// Include extra field values in filter haystack
			for _, v := range b.Extra {
				haystack += " " + strings.ToLower(v)
			}
			if strings.Contains(haystack, needle) {
				out = append(out, b)
			}
		}
		m.filtered = out
	}
	m.cursor = 0
	m.offset = 0
}

func (m BuildsListModel) View() string {
	var b strings.Builder

	// Title line with count
	total := len(m.allBuilds)
	showing := len(m.filtered)
	if m.filter == "" {
		b.WriteString(HeaderStyle.Render(fmt.Sprintf("Builds (%d)", total)))
	} else {
		b.WriteString(HeaderStyle.Render(fmt.Sprintf("Builds (%d of %d)", showing, total)))
	}
	b.WriteString("\n\n")

	// Table
	if len(m.filtered) == 0 {
		b.WriteString(DimStyle.Render("  No matching builds"))
		b.WriteString("\n")
	} else {
		b.WriteString(RenderBuildsTable(m.filtered, m.cursor, m.offset, m.height, m.extraColumns))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	// Filter bar
	if m.filtering {
		b.WriteString(fmt.Sprintf("  Filter: %s", m.filter))
		b.WriteString(DimStyle.Render("█"))
		b.WriteString("\n")
	} else if m.filter != "" {
		b.WriteString(DimStyle.Render(fmt.Sprintf("  Filter: %s", m.filter)))
		b.WriteString("\n")
	}

	// Help line
	if m.filtering {
		b.WriteString(DimStyle.Render("  enter/esc done  type to filter"))
	} else {
		parts := []string{"/ filter", "↑↓ scroll"}
		if m.filter != "" {
			parts = append(parts, "esc clear")
		}
		parts = append(parts, "q quit")
		b.WriteString(DimStyle.Render("  " + strings.Join(parts, "  ")))
	}
	b.WriteString("\n")

	return b.String()
}
