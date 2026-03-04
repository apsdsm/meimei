package tui

import (
	"context"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/spinner"
	meimeiaws "github.com/apsdsm/meimei/internal/aws"
	"github.com/apsdsm/meimei/internal/config"
	"github.com/apsdsm/meimei/internal/types"
)

type phase int

const (
	phaseLoading phase = iota
	phaseTargetSelect
	phaseBuildSelect
	phaseConfirm
	phaseDeployInit
	phaseDeploying
	phaseDone
)

type DeployModel struct {
	phase        phase
	client       *meimeiaws.Client
	filter       types.BuildFilter
	extraColumns []config.ExtraColumn
	spinner      spinner.Model
	width        int
	height       int

	// Loaded data
	groups []types.DeploymentGroup
	builds []types.Build

	// User selections
	selectedTargets []types.DeploymentGroup
	selectedBuild   types.Build

	// Child models
	targetModel   TargetModel
	buildModel    BuildModel
	confirmModel  ConfirmModel
	progressModel ProgressModel

	err error
}

func NewDeployModel(client *meimeiaws.Client, filter types.BuildFilter, extraColumns []config.ExtraColumn) DeployModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = SpinnerStyle

	return DeployModel{
		phase:        phaseLoading,
		client:       client,
		filter:       filter,
		extraColumns: extraColumns,
		spinner:      s,
	}
}

func (m DeployModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.loadData())
}

func (m DeployModel) loadData() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		type groupResult struct {
			groups []types.DeploymentGroup
			err    error
		}
		type buildResult struct {
			builds []types.Build
			err    error
		}

		grCh := make(chan groupResult, 1)
		brCh := make(chan buildResult, 1)

		go func() {
			groups, err := m.client.ListDeploymentGroups(ctx, m.client.Account.CodeDeployAppName)
			grCh <- groupResult{groups, err}
		}()

		go func() {
			builds, err := m.client.QueryBuilds(ctx, m.client.Account.BuildsTable, m.client.Account.CodeDeployAppName, m.filter)
			brCh <- buildResult{builds, err}
		}()

		gr := <-grCh
		if gr.err != nil {
			return LoadedMsg{Err: gr.err}
		}

		br := <-brCh
		if br.err != nil {
			return LoadedMsg{Err: br.err}
		}

		return LoadedMsg{Groups: gr.groups, Builds: br.builds}
	}
}

func (m DeployModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case CancelMsg:
		return m, tea.Quit

	case LoadedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, tea.Quit
		}
		m.groups = msg.Groups
		m.builds = msg.Builds
		m.phase = phaseTargetSelect
		m.targetModel = NewTargetModel(m.groups)
		return m, nil

	case TargetsSelectedMsg:
		m.selectedTargets = msg.Groups
		m.phase = phaseBuildSelect
		m.buildModel = NewBuildModel(m.builds, m.height, m.extraColumns)
		return m, nil

	case BuildSelectedMsg:
		m.selectedBuild = msg.Build
		m.phase = phaseConfirm
		user := os.Getenv("USER")
		m.confirmModel = NewConfirmModel(m.selectedTargets, m.selectedBuild, user)
		return m, nil

	case ConfirmedMsg:
		m.phase = phaseDeployInit
		return m, tea.Batch(m.spinner.Tick, m.initDeployments())

	case DeployInitiatedMsg:
		if msg.Err != nil {
			m.err = msg.Err
			return m, tea.Quit
		}
		m.phase = phaseDeploying
		m.progressModel = NewProgressModel(msg.Statuses)
		return m, m.progressModel.Init()

	case pollTickMsg:
		if m.phase == phaseDeploying {
			return m, m.pollStatuses()
		}

	case DeployStatusUpdateMsg:
		pm, cmd := m.progressModel.Update(msg)
		m.progressModel = pm
		if m.progressModel.Done() {
			m.phase = phaseDone
		}
		return m, cmd
	}

	// Delegate to active child model
	switch m.phase {
	case phaseLoading, phaseDeployInit:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case phaseTargetSelect:
		tm, cmd := m.targetModel.Update(msg)
		m.targetModel = tm
		return m, cmd

	case phaseBuildSelect:
		bm, cmd := m.buildModel.Update(msg)
		m.buildModel = bm
		return m, cmd

	case phaseConfirm:
		cm, cmd := m.confirmModel.Update(msg)
		m.confirmModel = cm
		return m, cmd

	case phaseDeploying, phaseDone:
		pm, cmd := m.progressModel.Update(msg)
		m.progressModel = pm
		return m, cmd
	}

	return m, nil
}

func (m DeployModel) View() string {
	if m.err != nil {
		return ErrorStyle.Render("Error: "+m.err.Error()) + "\n"
	}

	switch m.phase {
	case phaseLoading:
		return m.spinner.View() + " Loading deployment groups and builds...\n"
	case phaseDeployInit:
		return m.spinner.View() + " Starting deployment...\n"
	case phaseTargetSelect:
		return m.targetModel.View()
	case phaseBuildSelect:
		return m.buildModel.View()
	case phaseConfirm:
		return m.confirmModel.View()
	case phaseDeploying, phaseDone:
		return m.progressModel.View()
	}

	return ""
}

func (m DeployModel) initDeployments() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		user := os.Getenv("USER")
		var statuses []types.DeploymentStatus

		for _, target := range m.selectedTargets {
			deployID, err := m.client.CreateDeployment(
				ctx,
				m.client.Account.CodeDeployAppName,
				target.FullName,
				m.selectedBuild.Bucket,
				m.selectedBuild.Key,
				m.selectedBuild.BuildID,
				user,
			)
			if err != nil {
				return DeployInitiatedMsg{Err: err}
			}

			statuses = append(statuses, types.DeploymentStatus{
				GroupName:    target.FullName,
				DeploymentID: deployID,
				Status:       "Created",
			})
		}

		return DeployInitiatedMsg{Statuses: statuses}
	}
}

func (m DeployModel) pollStatuses() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		statuses := m.progressModel.Statuses()
		updated := make([]types.DeploymentStatus, len(statuses))
		copy(updated, statuses)

		for i, s := range updated {
			switch s.Status {
			case "Succeeded", "Failed", "Stopped":
				continue
			}

			status, err := m.client.GetDeploymentStatus(ctx, s.DeploymentID)
			if err != nil {
				return DeployStatusUpdateMsg{Err: err}
			}
			updated[i].Status = status
		}

		return DeployStatusUpdateMsg{Statuses: updated}
	}
}
