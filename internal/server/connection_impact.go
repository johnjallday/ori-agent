package server

import (
	"context"

	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// connectionImpactEnumerator resolves which workspaces use each Google product
// grant for the disconnect impact preview (FR 77). It reads only safe references
// — an MCP server name for Calendar/Drive, a google-connection email account for
// Gmail — never a credential (FR 76).
//
// It holds the builder rather than the stores directly so it reads
// b.workspaceStore / b.vaultStore lazily at request time: those are wired in a
// later builder phase than the connections handler, so capturing them at wiring
// time would capture nil (see the builder-phase-wiring-order gotcha).
type connectionImpactEnumerator struct{ b *ServerBuilder }

const googleConnectionEmailSource = "google-connection"

func (e connectionImpactEnumerator) WorkspacesUsingProduct(ctx context.Context, product connections.ProductKey, credentialRef string) ([]connectionshttp.WorkspaceImpact, error) {
	store := e.b.workspaceStore
	if store == nil {
		return nil, nil
	}
	all, err := store.ListActive()
	if err != nil {
		return nil, err
	}
	out := make([]connectionshttp.WorkspaceImpact, 0, len(all))
	for _, ws := range all {
		if ws == nil {
			continue
		}
		if e.workspaceUsesProduct(ctx, ws, product, credentialRef) {
			out = append(out, connectionshttp.WorkspaceImpact{ID: ws.ID, Name: ws.Name})
		}
	}
	return out, nil
}

func (e connectionImpactEnumerator) workspaceUsesProduct(ctx context.Context, ws *workspace.Workspace, product connections.ProductKey, credentialRef string) bool {
	switch product {
	case connections.ProductCalendar, connections.ProductDrive:
		// Calendar/Drive are exposed through an MCP server binding; the grant's
		// credentialRef is that server name.
		if credentialRef == "" {
			return false
		}
		for _, b := range ws.GetMCPBindings() {
			if b.ServerName == credentialRef {
				return true
			}
		}
	case connections.ProductGmail:
		// Gmail reuse is a workspace-scoped vault EmailAccount minted from the
		// connection (source "google-connection").
		if e.b.vaultStore == nil {
			return false
		}
		accounts, err := e.b.vaultStore.ListEmailAccounts(ctx, "", ws.ID)
		if err != nil {
			return false
		}
		for _, a := range accounts {
			if a.Source == googleConnectionEmailSource {
				return true
			}
		}
	}
	return false
}
