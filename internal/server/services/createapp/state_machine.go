package createapp

type DeployRunStatus string

const (
	DeployRunQueued       DeployRunStatus = "queued"
	DeployRunPreparing    DeployRunStatus = "preparing"
	DeployRunBuilding     DeployRunStatus = "building"
	DeployRunPushing      DeployRunStatus = "pushing"
	DeployRunRendering    DeployRunStatus = "rendering"
	DeployRunSyncing      DeployRunStatus = "syncing"
	DeployRunLive         DeployRunStatus = "live"
	DeployRunCompensating DeployRunStatus = "compensating"
	DeployRunRolledBack   DeployRunStatus = "rolled_back"
	DeployRunFailed       DeployRunStatus = "failed"
)

func CreateAppStateSequence() []DeployRunStatus {
	return []DeployRunStatus{
		DeployRunQueued,
		DeployRunPreparing,
		DeployRunBuilding,
		DeployRunPushing,
		DeployRunRendering,
		DeployRunSyncing,
		DeployRunLive,
	}
}
