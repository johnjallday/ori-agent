package externalmemory

import (
	"sort"
	"strings"
)

// The reverse of the account paste (FR22): Ori renders the user's approved
// Personal HQ memory and profile in the five-category text claude.ai's
// "Start import" box expects, one "[YYYY-MM-DD] - text" line per fact.

// ClaudeImportSections are the import format's categories, in order.
var ClaudeImportSections = []string{"Instructions", "Identity", "Career", "Projects", "Preferences"}

// ExportProfile is the part of the global profile that is exported.
type ExportProfile struct {
	Name string
	Role string
	// TimeZone stands in for a location: the profile records no place.
	TimeZone        string
	Language        string
	About           string
	Specializations []string
	// Preferences are the profile's saved communication preferences
	// (key → value); they are instructions to an assistant.
	Preferences map[string]string
	// Date is YYYY-MM-DD for every profile line, or "" when unknown.
	Date string
}

// ExportFact is one approved Personal HQ fact.
type ExportFact struct {
	// Category is the Personal HQ category (how_you_work, people, projects,
	// routines, sources).
	Category string
	Text     string
	// Date is YYYY-MM-DD when the fact was approved, or "".
	Date string
	// FromWorkspace marks a fact promoted from a workspace's memory.
	FromWorkspace bool
}

type exportLine struct {
	date string
	text string
}

// RenderClaudeImport renders the import text. Each section lists its lines
// oldest first ("[unknown]" when undated, after the dated ones); an empty
// section keeps its heading, as the format shows all five.
func RenderClaudeImport(profile ExportProfile, facts []ExportFact) string {
	sections := map[string][]exportLine{}
	add := func(section, date, text string) {
		text = collapseSpace(text)
		if text == "" {
			return
		}
		sections[section] = append(sections[section], exportLine{date: date, text: text})
	}

	keys := make([]string, 0, len(profile.Preferences))
	for key := range profile.Preferences {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value := strings.TrimSpace(profile.Preferences[key]); value != "" {
			add("Instructions", profile.Date, humanizeKey(key)+": "+value)
		}
	}
	if profile.Name != "" {
		add("Identity", profile.Date, "Name: "+profile.Name)
	}
	if profile.Role != "" {
		add("Identity", profile.Date, "Role: "+profile.Role)
	}
	if profile.TimeZone != "" {
		add("Identity", profile.Date, "Time zone: "+profile.TimeZone)
	}
	if profile.Language != "" {
		add("Identity", profile.Date, "Language: "+profile.Language)
	}
	if len(profile.Specializations) > 0 {
		add("Career", profile.Date, "Specializes in "+strings.Join(profile.Specializations, ", "))
	}
	if profile.About != "" {
		add("Career", profile.Date, profile.About)
	}
	for _, fact := range facts {
		switch {
		case fact.Category == "projects" || fact.FromWorkspace:
			add("Projects", fact.Date, fact.Text)
		default:
			add("Preferences", fact.Date, fact.Text)
		}
	}

	var b strings.Builder
	for i, section := range ClaudeImportSections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## " + section + "\n")
		lines := sections[section]
		sort.SliceStable(lines, func(i, j int) bool {
			a, c := lines[i].date, lines[j].date
			if (a == "") != (c == "") {
				return a != ""
			}
			return a < c
		})
		for _, line := range lines {
			date := line.date
			if date == "" {
				date = "unknown"
			}
			b.WriteString("[" + date + "] - " + line.text + "\n")
		}
	}
	return b.String()
}

// humanizeKey turns a preference key ("response_style") into words
// ("Response style").
func humanizeKey(key string) string {
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(key)))
	if len(words) == 0 {
		return "Preference"
	}
	text := strings.Join(words, " ")
	return strings.ToUpper(text[:1]) + text[1:]
}
