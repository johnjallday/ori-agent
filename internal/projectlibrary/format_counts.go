package projectlibrary

// FormatCounts reports how many of the library's entries hold a project of
// each catalog format. An entry observed in two formats counts once for each.
// It reads the document only: no folder, no file.
func (d Document) FormatCounts() map[string]int {
	counts := make(map[string]int)
	for _, entry := range d.Entries {
		seen := make(map[string]bool, len(entry.Observations))
		for _, observation := range entry.Observations {
			if observation.Format == "" || seen[observation.Format] {
				continue
			}
			seen[observation.Format] = true
			counts[observation.Format]++
		}
	}
	return counts
}
