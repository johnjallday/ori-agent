package filejanitor

import (
	"slices"
	"sort"
)

// A route is a connection that is on right now, as the review and the press see
// it: the receiver's identity and folder, and the kinds it carries. Routes are
// worked out once per request, outside any scan-state transaction, and passed in
// as a value: the draft revision, the plan digest and the press all bind the same
// routes, and the press locks exactly the receivers it uses.
//
// A send in the saved choices is a positive mark on a move choice: the connection
// ID. It counts only while that connection is a route that carries the file's
// kind; otherwise the same choice is an ordinary move into the sender's Filed
// folder. Losing the mark can therefore only turn a send into a keep.

type sendRoute struct {
	ConnectionID        string
	ReceiverWorkspaceID string
	ReceiverName        string
	Kinds               []Category
	ReceiverRootID      string
	ReceiverRootDirID   string
	SenderRootDirID     string
	// receiverRoot is the receiver's managed folder. It is used to move and to
	// check a name, and is never serialized, hashed or shown.
	receiverRoot string
}

// Carries reports whether the route carries files of kind.
func (r sendRoute) Carries(kind Category) bool { return slices.Contains(r.Kinds, kind) }

type sendRoutes []sendRoute

// byID finds the route of one connection.
func (r sendRoutes) byID(connectionID string) (sendRoute, bool) {
	for _, route := range r {
		if route.ConnectionID == connectionID {
			return route, true
		}
	}
	return sendRoute{}, false
}

// carrying finds the route that carries kind. A kind goes to at most one
// receiver, so there is at most one.
func (r sendRoutes) carrying(kind Category) (sendRoute, bool) {
	for _, route := range r {
		if route.Carries(kind) {
			return route, true
		}
	}
	return sendRoute{}, false
}

// sends reports whether a move choice for a file of kind, marked with
// connectionID, is a send right now.
func (r sendRoutes) sends(connectionID string, kind Category) bool {
	if connectionID == "" {
		return false
	}
	route, found := r.byID(connectionID)
	return found && route.Carries(kind)
}

// key is what the revision and the digest bind: every identity of every route,
// and no name or path. Two key()s are equal exactly when the same connections are
// on between the same folders.
func (r sendRoutes) key() [][]string {
	out := make([][]string, 0, len(r))
	for _, route := range r {
		kinds := make([]string, len(route.Kinds))
		for i, kind := range route.Kinds {
			kinds[i] = string(kind)
		}
		row := []string{route.ConnectionID, route.ReceiverWorkspaceID, route.ReceiverRootID, route.ReceiverRootDirID, route.SenderRootDirID}
		out = append(out, append(row, kinds...))
	}
	return out
}

// receiverRoots are the receivers' folders, for lockRoots.
func (r sendRoutes) receiverRoots() []string {
	roots := make([]string, 0, len(r))
	for _, route := range r {
		roots = append(roots, route.receiverRoot)
	}
	return roots
}

// sameRoutes reports whether two route sets are the same connections between the
// same folders, at the same paths.
func sameRoutes(a, b sendRoutes) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !slices.Equal(a.key()[i], b.key()[i]) || a[i].receiverRoot != b[i].receiverRoot {
			return false
		}
	}
	return true
}

// liveSendRoutes works out the sender's routes as they are now. A workspace with
// no connection costs one settings read and touches no folder.
func (s *Service) liveSendRoutes(owner, workspaceID string) sendRoutes {
	if s == nil || s.store == nil {
		return nil
	}
	settings, err := s.store.LoadSettings(workspaceID)
	if err != nil || len(settings.SendsTo) == 0 {
		return nil
	}
	sender, senderErr := s.loadSide(owner, workspaceID, true)
	if senderErr != nil {
		return nil
	}
	var routes sendRoutes
	for _, connection := range sender.settings.SendsTo {
		receiver, on, _ := s.checkConnection(owner, sender, nil, connection)
		if !on {
			continue
		}
		routes = append(routes, sendRoute{
			ConnectionID: connection.ID, ReceiverWorkspaceID: connection.ReceiverWorkspaceID, ReceiverName: receiver.name,
			Kinds:          append([]Category(nil), connection.Kinds...),
			ReceiverRootID: connection.ReceiverRootID, ReceiverRootDirID: connection.ReceiverRootDirID,
			SenderRootDirID: connection.SenderRootDirID, receiverRoot: receiver.root.Path,
		})
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].ConnectionID < routes[j].ConnectionID })
	return routes
}
