package store

import (
	"sort"
	"strings"
)

// RetiredAssistantRecords names the stored records of Ori's retired protected
// system assistant that no list or lookup reaches (see fileStore.GetAgent). A
// name is reported only while no visible agent holds it: once a person has
// their own agent under that name, the name is theirs and nothing may treat a
// reference to it as the retired record.
//
// Identification is by the record's durable marker, never by name.
type RetiredAssistantRecords interface {
	RetiredAssistantRecordNames() []string
}

// RetiredAssistantRecordNames reports the hidden retired-assistant records in
// st, or nil when st cannot tell.
func RetiredAssistantRecordNames(st Store) []string {
	reader, ok := st.(RetiredAssistantRecords)
	if !ok {
		return nil
	}
	return reader.RetiredAssistantRecordNames()
}

func (s *fileStore) RetiredAssistantRecordNames() []string {
	s.mu.Lock()
	marked := make([]string, 0, 1)
	for name, ag := range s.agents {
		if isMarkedSystemAgent(ag) {
			marked = append(marked, name)
		}
	}
	s.mu.Unlock()
	sort.Strings(marked)
	return marked
}

func (c *CompositeStore) RetiredAssistantRecordNames() []string {
	system, ok := c.system.(RetiredAssistantRecords)
	if !ok {
		return nil
	}
	var out []string
	for _, name := range system.RetiredAssistantRecordNames() {
		// A person's own agent of the same name, anywhere, owns the name.
		if _, visible := c.GetAgent(strings.TrimSpace(name)); visible {
			continue
		}
		out = append(out, name)
	}
	return out
}
