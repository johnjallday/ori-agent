package workspace

import "context"

// SongFactsReader gives a ticket the facts the owner's Home learned about the
// songs of the folder the ticket runs in. It is a one-way seam: workspace code
// must not depend on the folder agent, so the server supplies the reader.
//
// SongFacts answers for one ticket, by the workspace it runs in and the agent
// that works it. It is empty for every ticket that is not worked by that
// folder's own agent, so no other agent is given a track or marker name, and
// empty whenever the Home's song-details switch is off. Its text is reference
// data the reader has already fenced; the caller adds it to the prompt as is.
type SongFactsReader interface {
	SongFacts(ctx context.Context, workspaceID, agentName string) string
}
