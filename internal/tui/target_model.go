package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/apsdsm/meimei/internal/types"
)

type TargetModel struct {
	groups      []types.DeploymentGroup
	orderedEnvs []string
	columns     [][]types.DeploymentGroup // columns[colIdx][rowIdx]
	selected    map[string]bool
	curCol      int
	curRow      int
}

func NewTargetModel(groups []types.DeploymentGroup) TargetModel {
	// Collect unique envs
	envSet := map[string]bool{}
	envOrder := []string{}
	for _, g := range groups {
		if !envSet[g.Env] {
			envSet[g.Env] = true
			envOrder = append(envOrder, g.Env)
		}
	}

	// Order: dev, stg, prd first, then remaining alphabetically
	priority := map[string]int{"dev": 0, "stg": 1, "prd": 2}
	var ordered, other []string
	for _, e := range envOrder {
		if _, ok := priority[e]; ok {
			ordered = append(ordered, e)
		} else {
			other = append(other, e)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		return priority[ordered[i]] < priority[ordered[j]]
	})
	sort.Strings(other)
	ordered = append(ordered, other...)

	// Build columns
	columns := make([][]types.DeploymentGroup, len(ordered))
	// Sort groups by full name first so column ordering is stable
	sorted := make([]types.DeploymentGroup, len(groups))
	copy(sorted, groups)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].FullName < sorted[j].FullName
	})

	envIdx := map[string]int{}
	for i, e := range ordered {
		envIdx[e] = i
	}
	for _, g := range sorted {
		ci := envIdx[g.Env]
		columns[ci] = append(columns[ci], g)
	}

	return TargetModel{
		groups:      groups,
		orderedEnvs: ordered,
		columns:     columns,
		selected:    make(map[string]bool),
		curCol:      0,
		curRow:      0,
	}
}

func (m TargetModel) Init() tea.Cmd {
	return nil
}

func (m TargetModel) Update(msg tea.Msg) (TargetModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.curRow > 0 {
				m.curRow--
			}
		case "down", "j":
			if m.curRow < len(m.columns[m.curCol])-1 {
				m.curRow++
			}
		case "left", "h":
			if m.curCol > 0 {
				m.curCol--
				if m.curRow >= len(m.columns[m.curCol]) {
					m.curRow = len(m.columns[m.curCol]) - 1
				}
			}
		case "right", "l":
			if m.curCol < len(m.columns)-1 {
				m.curCol++
				if m.curRow >= len(m.columns[m.curCol]) {
					m.curRow = len(m.columns[m.curCol]) - 1
				}
			}
		case " ":
			g := m.columns[m.curCol][m.curRow]
			m.selected[g.FullName] = !m.selected[g.FullName]
		case "a":
			for _, g := range m.columns[m.curCol] {
				m.selected[g.FullName] = true
			}
		case "n":
			for _, g := range m.columns[m.curCol] {
				delete(m.selected, g.FullName)
			}
		case "enter":
			var chosen []types.DeploymentGroup
			for _, col := range m.columns {
				for _, g := range col {
					if m.selected[g.FullName] {
						chosen = append(chosen, g)
					}
				}
			}
			if len(chosen) > 0 {
				return m, func() tea.Msg { return TargetsSelectedMsg{Groups: chosen} }
			}
		case "q", "ctrl+c":
			return m, func() tea.Msg { return CancelMsg{} }
		}
	}
	return m, nil
}

func (m TargetModel) View() string {
	var b strings.Builder

	// Column headers
	for _, env := range m.orderedEnvs {
		header := strings.ToUpper(env)
		cell := HeaderStyle.Render(fmt.Sprintf("  %-*s", ColWidth-2, header))
		b.WriteString(lipgloss.NewStyle().Width(ColWidth).Render(cell))
	}
	b.WriteString("\n")

	// Find max rows
	maxRows := 0
	for _, col := range m.columns {
		if len(col) > maxRows {
			maxRows = len(col)
		}
	}

	// Grid rows
	for r := 0; r < maxRows; r++ {
		for c, col := range m.columns {
			if r < len(col) {
				g := col[r]
				check := " "
				if m.selected[g.FullName] {
					check = "x"
				}
				cell := fmt.Sprintf("  [%s] %s", check, g.Cluster)

				if c == m.curCol && r == m.curRow {
					b.WriteString(CursorStyle.Render(fmt.Sprintf("%-*s", ColWidth, cell)))
				} else if m.selected[g.FullName] {
					b.WriteString(SelectedStyle.Render(fmt.Sprintf("%-*s", ColWidth, cell)))
				} else {
					b.WriteString(fmt.Sprintf("%-*s", ColWidth, cell))
				}
			} else {
				b.WriteString(fmt.Sprintf("%-*s", ColWidth, ""))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(DimStyle.Render("  ↑↓ move  ←→ column  SPACE toggle  a all  n none  ENTER confirm  q quit"))
	b.WriteString("\n")

	return b.String()
}
