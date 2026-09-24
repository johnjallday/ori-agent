package workspace

import "context"

// ReviewedMemoryReader projects only currently eligible canonical HQ facts for
// a server-verified user, workspace and responding agent instance. It is a
// one-way seam: workspace/chat code must not depend on the personalassistant
// package or infer approval by reading raw MEMORY.md.
type ReviewedMemoryReader interface {
	Section(ctx context.Context, userID, workspaceID, agentInstanceID string) (string, error)
}
