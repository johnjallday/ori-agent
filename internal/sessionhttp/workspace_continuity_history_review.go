package sessionhttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

// inspectOwnedHistory validates typed owner/relationship evidence before
// describing a modern member's history as verified in the read-only import
// review. Digest and count checks alone do not make a syntactically valid
// chunk restorable. This does NOT grant admission or claim unsupported domains
// are empty; a confirmed import must recheck within its own operation lease.
func inspectOwnedHistory(ctx context.Context, dir string, inspected workspacecontinuity.Inspection) error {
	owner := inspected.Manifest.WorkspaceID
	components := make(map[string]workspacecontinuity.Component, len(inspected.Manifest.Components))
	for _, component := range inspected.Manifest.Components {
		components[component.Domain] = component
	}
	sessions := map[string]int{}
	messageCounts := map[string]int{}
	messages := map[string]string{}
	if components["sessions"].Availability == workspacecontinuity.Present {
		if err := workspacecontinuity.ReadComponentRecords(ctx, dir, inspected, "sessions", func(chunk workspacecontinuity.Chunk) error {
			for _, record := range chunk.Records {
				switch chunk.Family {
				case "sessions":
					value, err := session.DecodeContinuitySession(record, owner)
					if err != nil {
						return err
					}
					sessions[value.ID] = value.MessageCount
				case "messages":
					value, err := session.DecodeContinuityMessage(record, owner)
					if err != nil {
						return err
					}
					messageCounts[value.Message.SessionID]++
					messages[value.Message.ID] = value.Message.SessionID
				default:
					return workspacecontinuity.ErrInvalid
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if len(sessions) == 0 {
			return workspacecontinuity.ErrIncomplete
		}
		for id, count := range sessions {
			if messageCounts[id] != count {
				return workspacecontinuity.ErrIncomplete
			}
			delete(messageCounts, id)
		}
		if len(messageCounts) != 0 {
			return workspacecontinuity.ErrIncomplete
		}
	}
	if components["tool_history"].Availability == workspacecontinuity.Present {
		if err := workspacecontinuity.ReadComponentRecords(ctx, dir, inspected, "tool_history", func(chunk workspacecontinuity.Chunk) error {
			if chunk.Family != "tool_calls" {
				return workspacecontinuity.ErrInvalid
			}
			for _, record := range chunk.Records {
				value, err := session.DecodeContinuityToolCall(record, owner)
				if err != nil {
					return err
				}
				if _, ok := sessions[value.Call.SessionID]; !ok {
					return workspacecontinuity.ErrIncomplete
				}
				if value.Call.MessageID != "" && messages[value.Call.MessageID] != value.Call.SessionID {
					return workspacecontinuity.ErrIncomplete
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if components["uploads"].Availability == workspacecontinuity.Present {
		declared := map[workspacecontinuity.BlobRef]bool{}
		for _, ref := range components["uploads"].Blobs {
			declared[ref] = false
		}
		paths := map[string]bool{}
		if err := workspacecontinuity.ReadComponentRecords(ctx, dir, inspected, "uploads", func(chunk workspacecontinuity.Chunk) error {
			if chunk.Family != "uploads" {
				return workspacecontinuity.ErrInvalid
			}
			for _, record := range chunk.Records {
				value, err := sessionfiles.DecodeContinuityUpload(record, owner)
				if err != nil {
					return err
				}
				if _, ok := sessions[value.SessionID]; !ok {
					return workspacecontinuity.ErrIncomplete
				}
				path := strings.ToLower(value.SessionID + "/" + value.Entry.Path)
				if paths[path] {
					return workspacecontinuity.ErrInvalid
				}
				paths[path] = true
				if value.Blob != nil {
					if _, ok := declared[*value.Blob]; !ok {
						return workspacecontinuity.ErrIncomplete
					}
					declared[*value.Blob] = true
				}
			}
			return nil
		}); err != nil {
			return err
		}
		for _, used := range declared {
			if !used {
				return workspacecontinuity.ErrIncomplete
			}
		}
	}
	for _, domain := range []string{"followups", "brief_config", "brief_history"} {
		if components[domain].Availability != workspacecontinuity.Present {
			continue
		}
		if err := workspacecontinuity.ReadComponentRecords(ctx, dir, inspected, domain, func(chunk workspacecontinuity.Chunk) error {
			for _, record := range chunk.Records {
				switch domain + "/" + chunk.Family {
				case "followups/followups":
					if _, err := followup.DecodeContinuityFollowUp(record, owner); err != nil {
						return err
					}
				case "brief_config/configs":
					config, err := dailybrief.DecodeContinuityConfig(record)
					if err != nil {
						return err
					}
					if config.WorkspaceID != owner {
						return workspacecontinuity.ErrInvalid
					}
				case "brief_history/revisions":
					// An unfinished source attempt restores as interrupted
					// history, never as a live claim.
					if _, err := dailybrief.DecodeContinuityRevision(record, owner); err != nil {
						return err
					}
				default:
					return workspacecontinuity.ErrInvalid
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
