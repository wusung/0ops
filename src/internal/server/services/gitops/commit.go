package gitops

import "fmt"

// String formats the commit message contract.
func (m CommitMessage) String() string {
	return fmt.Sprintf(
		"%s: %s/%s @ %s\n\nPreview-Id: %s\nTrace-Id: %s\n",
		m.Action, m.TeamSlug, m.AppSlug, m.DeployRunID, m.PreviewID, m.TraceID,
	)
}
