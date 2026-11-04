package workspace

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestWorkLabels_DisplayStringSitesNeverSayTask guards the server-produced
// display strings that name a work item (PRD one-vocabulary FR14). Each row is
// a file and the patterns that must not match it: a later edit that puts
// "task" back into a quest title or a result-group title fails here.
func TestWorkLabels_DisplayStringSitesNeverSayTask(t *testing.T) {
	// A display string: Title/Why fields and the default result-group titles.
	questCopy := regexp.MustCompile(`(?m)^\s*(Title|Why):\s*"[^"]*\b[Tt]asks?\b[^"]*"`)
	defaultGroupTitle := regexp.MustCompile(`(Title:|title =)\s*"Tasks"`)
	jsonCopy := regexp.MustCompile(`"(title|description|button|label|why)":\s*"[^"]*\b[Tt]asks?\b[^"]*"`)

	sites := []struct {
		file      string
		forbidden []*regexp.Regexp
	}{
		{"../progression/quests.go", []*regexp.Regexp{questCopy}},
		{"task_result_types.go", []*regexp.Regexp{defaultGroupTitle}},
		{"../orchestrationhttp/task_result_promote.go", []*regexp.Regexp{defaultGroupTitle}},
		{"../hostquests/email-ops-setup.json", []*regexp.Regexp{jsonCopy}},
	}
	for _, site := range sites {
		t.Run(filepath.Base(site.file), func(t *testing.T) {
			data, err := os.ReadFile(filepath.FromSlash(site.file))
			if err != nil {
				t.Fatalf("read %s: %v", site.file, err)
			}
			for _, re := range site.forbidden {
				for _, hit := range re.FindAllString(string(data), -1) {
					t.Errorf("%s shows a work item as a task: %s", site.file, hit)
				}
			}
		})
	}
}

func TestWorkLabels_Values(t *testing.T) {
	cases := []struct{ got, want string }{
		{LabelTicket, "Ticket"},
		{LabelTickets, "Tickets"},
		{LabelBacklog, "Backlog"},
		{LabelRunYourFirstTicket, "Run your first ticket"},
		{LabelGiveAnAgentATicket, "Give an agent a ticket"},
		{LabelResultGroupTitle, "Tickets"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("label = %q, want %q", c.got, c.want)
		}
	}
}
