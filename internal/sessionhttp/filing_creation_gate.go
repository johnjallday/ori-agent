package sessionhttp

import "context"

type filingCreationLockKey struct{}

// LockFilingCreation serializes normal built-in Filing creation with the
// expansion's existing-workspace check, reservation and create. The derived
// server-local context lets the same caller enter the normal create pipeline.
func (h *Handler) LockFilingCreation(ctx context.Context) (context.Context, func()) {
	if ctx.Value(filingCreationLockKey{}) == h {
		return ctx, func() {}
	}
	h.filingCreationMu.Lock()
	return context.WithValue(ctx, filingCreationLockKey{}, h), h.filingCreationMu.Unlock
}
