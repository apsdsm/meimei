package tui

import (
	"github.com/apsdsm/meimei/internal/types"
)

// Phase transition messages sent from child models to the parent orchestrator.

type LoadedMsg struct {
	Groups []types.DeploymentGroup
	Builds []types.Build
	Err    error
}

type TargetsSelectedMsg struct {
	Groups []types.DeploymentGroup
}

type BuildSelectedMsg struct {
	Build types.Build
}

type ConfirmedMsg struct{}

type DeployInitiatedMsg struct {
	Statuses []types.DeploymentStatus
	Err      error
}

type DeployStatusUpdateMsg struct {
	Statuses []types.DeploymentStatus
	Err      error
}

type CancelMsg struct{}
