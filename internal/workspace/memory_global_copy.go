package workspace

import "strings"

// GlobalCopyProvenance is the provenance of an entry copied from the global
// memory item itemID: "global:<item-id>".
func GlobalCopyProvenance(itemID string) string {
	return GlobalCopyProvenancePrefix + strings.TrimSpace(itemID)
}

// EntriesWithProvenance returns the entries whose provenance is exactly
// provenance (case-insensitive, like AppendUnique). It only reads: a
// workspace without MEMORY.md has no entries, and no file is created.
func (s *MemoryStore) EntriesWithProvenance(workspaceID, provenance string) ([]MemoryEntry, error) {
	provenance = strings.TrimSpace(provenance)
	raw, err := s.ReadRaw(workspaceID)
	if err != nil {
		return nil, err
	}
	var matches []MemoryEntry
	for _, entry := range ParseMemoryDocument(raw).Entries() {
		if strings.EqualFold(strings.TrimSpace(entry.Provenance), provenance) {
			matches = append(matches, entry)
		}
	}
	return matches, nil
}

// RemoveByProvenance removes exactly one ordinary entry carrying provenance,
// the first in file order, and returns it. Every other line is untouched.
// Managed HQ lines are never removed this way. ErrMemoryEntryNotFound means
// there is no such entry.
func (s *MemoryStore) RemoveByProvenance(workspaceID, provenance string) (MemoryEntry, error) {
	provenance = strings.TrimSpace(provenance)
	if provenance == "" || IsHQManagedProvenance(provenance) {
		return MemoryEntry{}, ErrMemoryEntryNotFound
	}
	defer notifyMemoryWritten(workspaceID) // after the unlock below
	memoryMu.Lock()
	defer memoryMu.Unlock()
	raw, path, err := s.readExact(workspaceID)
	if err != nil {
		return MemoryEntry{}, err
	}
	for _, line := range splitPhysicalMemoryLines(raw) {
		if line.entry == nil || !strings.EqualFold(strings.TrimSpace(line.entry.Provenance), provenance) {
			continue
		}
		if err := s.writeExact(path, removeMemoryLine(raw, line), hashMemoryBytes(raw)); err != nil {
			return MemoryEntry{}, err
		}
		return *line.entry, nil
	}
	return MemoryEntry{}, ErrMemoryEntryNotFound
}
