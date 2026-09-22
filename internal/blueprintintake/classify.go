package blueprintintake

import (
	"fmt"
	"strconv"
	"time"
)

func classifyReintake(items []ProposalItem, ledger []LedgerEntry, affectedSourceIDs map[string]bool) []ProposalItem {
	latest := make(map[string]LedgerEntry)
	for _, entry := range ledger {
		latest[ledgerKey(entry.Kind, entry.Key)] = entry
	}
	seen := make(map[string]bool)
	out := make([]ProposalItem, 0, len(items)+len(latest))
	for _, item := range items {
		key := ledgerKey(item.Kind, item.Key)
		seen[key] = true
		previous, found := latest[key]
		if !found {
			item.Classification = ProposalClassificationNew
			out = append(out, item)
			continue
		}
		item.Changes = proposalChanges(previous, item)
		if len(item.Changes) == 0 {
			item.Classification = ProposalClassificationUnchanged
		} else {
			item.Classification = ProposalClassificationChanged
		}
		out = append(out, item)
	}
	for key, previous := range latest {
		if seen[key] || previous.SourceID == "" || !affectedSourceIDs[previous.SourceID] {
			continue
		}
		item := proposalItemFromLedger(previous)
		item.Classification = ProposalClassificationNoLongerFound
		item.DisabledReason = "This item was not found in the refreshed source. Ori will not delete the existing record."
		out = append(out, item)
	}
	return out
}

func ledgerKey(kind, key string) string { return kind + "\x00" + key }

func proposalChanges(previous LedgerEntry, item ProposalItem) []ProposalChange {
	changes := make([]ProposalChange, 0, 4)
	appendChange := func(field, before, after string) {
		if before != after {
			changes = append(changes, ProposalChange{Field: field, Before: before, After: after})
		}
	}
	appendChange("title", previous.Title, item.Title)
	appendChange("description", previous.Description, item.Description)
	appendChange("text", previous.Text, item.Text)
	appendChange("memory type", previous.MemoryType, item.MemoryType)
	appendChange("body", previous.Body, item.Body)
	appendChange("due date", formatProposalTime(previous.DueAt), formatProposalTime(item.DueAt))
	appendChange("start", formatProposalTime(previous.Start), formatProposalTime(item.Start))
	appendChange("end", formatProposalTime(previous.End), formatProposalTime(item.End))
	if previous.AllDay != item.AllDay {
		appendChange("all day", strconv.FormatBool(previous.AllDay), strconv.FormatBool(item.AllDay))
	}
	appendChange("location", previous.Location, item.Location)
	return changes
}

func formatProposalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(time.RFC3339)
}

func proposalItemFromLedger(entry LedgerEntry) ProposalItem {
	return ProposalItem{
		Kind: entry.Kind, Key: entry.Key, Title: entry.Title, Description: entry.Description,
		Text: entry.Text, MemoryType: entry.MemoryType, Body: entry.Body, DueAt: entry.DueAt,
		Start: entry.Start, End: entry.End, AllDay: entry.AllDay, Location: entry.Location,
		Source: ProposalSource{SourceID: entry.SourceID, Quote: fmt.Sprintf("Previously applied from source %s.", entry.SourceID)},
	}
}
