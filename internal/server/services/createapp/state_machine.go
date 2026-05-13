package createapp

// DeployRunStage represents the create_app deploy lifecycle stage.
type DeployRunStage string

const (
	DeployRunQueued       DeployRunStage = "queued"
	DeployRunPreparing    DeployRunStage = "preparing"
	DeployRunBuilding     DeployRunStage = "building"
	DeployRunPushing      DeployRunStage = "pushing"
	DeployRunRendering    DeployRunStage = "rendering"
	DeployRunSyncing      DeployRunStage = "syncing"
	DeployRunLive         DeployRunStage = "live"
	DeployRunFailed       DeployRunStage = "failed"
	DeployRunCompensating DeployRunStage = "compensating"
	DeployRunRolledBack   DeployRunStage = "rolled_back"
)

// CreateAppLifecycle returns the forward state path for a successful create_app run.
func CreateAppLifecycle() []DeployRunStage {
	return []DeployRunStage{
		DeployRunQueued,
		DeployRunPreparing,
		DeployRunBuilding,
		DeployRunPushing,
		DeployRunRendering,
		DeployRunSyncing,
		DeployRunLive,
	}
}

// IsTerminal reports whether the stage ends the deploy_run lifecycle.
func IsTerminal(stage DeployRunStage) bool {
	switch stage {
	case DeployRunLive, DeployRunFailed, DeployRunRolledBack:
		return true
	default:
		return false
	}
}
