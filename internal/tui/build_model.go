package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/apsdsm/meimei/internal/config"
	"github.com/apsdsm/meimei/internal/types"
)

type BuildModel struct {
	allBuilds    []types.Build // full set
	filtered     []types.Build // after applying filter
	cursor       int
	offset       int
	height       int
	extraColumns []config.ExtraColumn

	filtering bool
	filter    string
}

func NewBuildModel(builds []types.Build, height int, extraColumns []config.ExtraColumn) BuildModel {
	viewHeight := height - 10 // title + table chrome + filter + help
	if viewHeight < 5 {
		viewHeight = 5
	}
	return BuildModel{
		allBuilds:    builds,
		filtered:     builds,
		height:       viewHeight,
		extraColumns: extraColumns,
	}
}

func (m BuildModel) Init() tea.Cmd {
	return nil
}

func (m BuildModel) Update(msg tea.Msg) (BuildModel, tea.Cmd) {
	switch msg := msg.(type) {
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
				return m, func() tea.Msg { return CancelMsg{} }
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
		case "enter":
			if len(m.filtered) > 0 {
				selected := m.filtered[m.cursor]
				return m, func() tea.Msg { return BuildSelectedMsg{Build: selected} }
			}
		case "q", "ctrl+c":
			return m, func() tea.Msg { return CancelMsg{} }
		}

	case tea.WindowSizeMsg:
		m.height = msg.Height - 10
		if m.height < 5 {
			m.height = 5
		}
	}
	return m, nil
}

func (m *BuildModel) applyFilter() {
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

func (m BuildModel) View() string {
	var b strings.Builder

	// Title with count
	total := len(m.allBuilds)
	showing := len(m.filtered)
	if m.filter == "" {
		b.WriteString(HeaderStyle.Render(fmt.Sprintf("Select a build (%d)", total)))
	} else {
		b.WriteString(HeaderStyle.Render(fmt.Sprintf("Select a build (%d of %d)", showing, total)))
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
		parts := []string{"/ filter", "↑↓ move", "enter select"}
		if m.filter != "" {
			parts = append(parts, "esc clear")
		}
		parts = append(parts, "q quit")
		b.WriteString(DimStyle.Render("  " + strings.Join(parts, "  ")))
	}
	b.WriteString("\n")

	return b.String()
}
