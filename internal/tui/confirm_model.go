package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/apsdsm/meimei/internal/types"
)

type ConfirmModel struct {
	targets []types.DeploymentGroup
	build   types.Build
	user    string
	input   string
}

func NewConfirmModel(targets []types.DeploymentGroup, build types.Build, user string) ConfirmModel {
	return ConfirmModel{
		targets: targets,
		build:   build,
		user:    user,
	}
}

func (m ConfirmModel) Init() tea.Cmd {
	return nil
}

func (m ConfirmModel) Update(msg tea.Msg) (ConfirmModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if m.input == "yes" {
				return m, func() tea.Msg { return ConfirmedMsg{} }
			}
		case "backspace":
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		case "ctrl+c":
			return m, func() tea.Msg { return CancelMsg{} }
		case "q":
			if m.input == "" {
				return m, func() tea.Msg { return CancelMsg{} }
			}
			m.input += "q"
		default:
			if len(msg.String()) == 1 {
				m.input += msg.String()
			}
		}
	}
	return m, nil
}

func (m ConfirmModel) View() string {
	var b strings.Builder

	b.WriteString(HeaderStyle.Render("Confirm deployment:"))
	b.WriteString("\n")
	b.WriteString("-------------------\n")

	// Build target summary
	targetLabel := buildTargetLabel(m.targets)
	b.WriteString(fmt.Sprintf(" deploy to: %s\n", targetLabel))
	for _, t := range m.targets {
		b.WriteString(fmt.Sprintf("            |- %s\n", t.FullName))
	}

	b.WriteString(fmt.Sprintf("     build: %s\n", m.build.BuildID))
	b.WriteString(fmt.Sprintf("will blame: %s\n", m.user))
	b.WriteString(fmt.Sprintf("    bucket: %s\n", m.build.Bucket))
	b.WriteString(fmt.Sprintf("       key: %s\n", m.build.Key))
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("Enter 'yes' to proceed: %s", m.input))
	b.WriteString(DimStyle.Render("█"))
	b.WriteString("\n")

	return b.String()
}

func buildTargetLabel(targets []types.DeploymentGroup) string {
	if len(targets) == 1 {
		return targets[0].FullName
	}

	// Group by env
	envGroups := map[string][]types.DeploymentGroup{}
	envOrder := []string{}
	for _, t := range targets {
		if _, ok := envGroups[t.Env]; !ok {
			envOrder = append(envOrder, t.Env)
		}
		envGroups[t.Env] = append(envGroups[t.Env], t)
	}

	var parts []string
	for _, env := range envOrder {
		groups := envGroups[env]
		// We don't know total count per env here, so just list them
		if len(groups) > 2 {
			parts = append(parts, fmt.Sprintf("%d × %s", len(groups), env))
		} else {
			for _, g := range groups {
				parts = append(parts, g.FullName)
			}
		}
	}
	return strings.Join(parts, ", ")
}
