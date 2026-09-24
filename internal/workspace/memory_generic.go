package workspace

import (
	"bytes"
	"fmt"
	"strings"
)

func validateGenericMemoryEntry(entry MemoryEntry) (MemoryEntry, error) {
	if IsHQManagedProvenance(entry.Provenance) {
		return MemoryEntry{}, ErrMemoryManaged
	}
	entry.Provenance = strings.TrimSpace(entry.Provenance)
	return validateExactMemoryEntry(entry)
}

func selectGenericMemoryLine(raw []byte, index int) (physicalMemoryLine, error) {
	var entries []physicalMemoryLine
	for _, line := range splitPhysicalMemoryLines(raw) {
		if line.entry != nil {
			entries = append(entries, line)
		}
	}
	if index < 0 || index >= len(entries) {
		return physicalMemoryLine{}, fmt.Errorf("%w: index %d, have %d entries", ErrMemoryIndexOutOfRange, index, len(entries))
	}
	if IsHQManagedProvenance(entries[index].entry.Provenance) {
		return physicalMemoryLine{}, ErrMemoryManaged
	}
	return entries[index], nil
}

func removeMemoryLine(raw []byte, line physicalMemoryLine) []byte {
	result := make([]byte, 0, len(raw)-(line.end-line.start))
	result = append(result, raw[:line.start]...)
	return append(result, raw[line.end:]...)
}

// appendMemoryBytes preserves the entire old file and its terminal-newline
// convention, including all managed lines a generic append must not rewrite.
func appendMemoryBytes(raw []byte, entry MemoryEntry) []byte {
	ending := []byte("\n")
	crlf := bytes.Count(raw, []byte("\r\n"))
	if crlf > bytes.Count(raw, []byte("\n"))-crlf {
		ending = []byte("\r\n")
	}
	result := make([]byte, 0, len(raw)+len(entry.Render())+len(ending)+24)
	if len(raw) == 0 {
		result = append(result, []byte("# Workspace Memory\n\n")...)
		result = append(result, entry.Render()...)
		return append(result, '\n')
	}
	result = append(result, raw...)
	if !bytes.HasSuffix(raw, []byte("\n")) {
		result = append(result, ending...)
	}
	result = append(result, entry.Render()...)
	if bytes.HasSuffix(raw, []byte("\n")) {
		result = append(result, ending...)
	}
	return result
}
