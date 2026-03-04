package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/apsdsm/meimei/internal/types"
)

type ProgressModel struct {
	statuses []types.DeploymentStatus
	spinner  spinner.Model
	done     bool
	ticks    int
}

func NewProgressModel(statuses []types.DeploymentStatus) ProgressModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = SpinnerStyle

	return ProgressModel{
		statuses: statuses,
		spinner:  s,
	}
}

func (m ProgressModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.pollTick())
}

type pollTickMsg time.Time

func (m ProgressModel) pollTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return pollTickMsg(t)
	})
}

func (m ProgressModel) Update(msg tea.Msg) (ProgressModel, tea.Cmd) {
	switch msg := msg.(type) {
	case pollTickMsg:
		if m.done {
			return m, nil
		}
		m.ticks++
		// Signal the parent to poll status
		return m, func() tea.Msg { return pollTickMsg(time.Now()) }

	case DeployStatusUpdateMsg:
		if msg.Err != nil {
			return m, nil
		}
		m.statuses = msg.Statuses
		m.done = allTerminal(m.statuses)
		if m.done {
			return m, nil
		}
		return m, m.pollTick()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, func() tea.Msg { return CancelMsg{} }
		}
	}
	return m, nil
}

func (m ProgressModel) View() string {
	var b strings.Builder

	if m.done {
		b.WriteString(HeaderStyle.Render("Deployment complete!"))
	} else {
		b.WriteString(HeaderStyle.Render("Deploying..."))
		b.WriteString(" ")
		b.WriteString(m.spinner.View())
	}
	b.WriteString("\n\n")

	for _, s := range m.statuses {
		statusStr := s.Status
		switch s.Status {
		case "Succeeded":
			statusStr = SuccessStyle.Render(s.Status)
		case "Failed", "Stopped":
			statusStr = ErrorStyle.Render(s.Status)
		}
		b.WriteString(fmt.Sprintf("  %-36s  %-14s  %s\n", s.GroupName, s.DeploymentID, statusStr))
	}

	if m.done {
		b.WriteString("\n")
		b.WriteString(DimStyle.Render("  Press q to exit"))
		b.WriteString("\n")
	}

	return b.String()
}

func allTerminal(statuses []types.DeploymentStatus) bool {
	for _, s := range statuses {
		switch s.Status {
		case "Succeeded", "Failed", "Stopped":
			continue
		default:
			return false
		}
	}
	return true
}

func (m ProgressModel) Statuses() []types.DeploymentStatus {
	return m.statuses
}

func (m ProgressModel) Done() bool {
	return m.done
}
