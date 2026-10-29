package grouprequirements

import "context"

// ObserveWorkspaceCreation reads the original grouped/standalone project
// creation journal. It does not Claim, replay/roll back a shell, Mark, prepare
// a Home, connect a project or execute staffing/setup. Resource/link proof is
// checked by the HTTP owner before any completed outcome is exposed.
func (s *Service) ObserveWorkspaceCreation(ctx context.Context, owner, token, key, inputDigest string) (Operation, Receipt, error) {
	return s.observeOperation(ctx, owner, token, key, inputDigest, OperationCreateWorkspace)
}
