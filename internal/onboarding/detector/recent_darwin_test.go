//go:build darwin

package detector

import (
	"os"
	"path/filepath"
	"testing"
)

// Fixture executables are the entire PATH: never query the test machine's real Spotlight history.
func TestUsageOnlySpotlightEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, status, rows string
		fail               bool
		want               int
	}{{"disabled", "Indexing disabled", "", true, 0}, {"empty", "Indexing enabled", "", false, 0}, {"observed", "Indexing enabled", "/Applications/Example Editor.app", false, 1}, {"failed", "Indexing enabled", "error", true, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			query := "printf '%s\\n' '" + tc.rows + "'"
			if tc.rows == "error" {
				query = "exit 2"
			}
			for name, script := range map[string]string{"mdutil": "echo '" + tc.status + "'", "mdfind": query} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			apps, err := NewDarwinDetector(Config{RecencyDays: 7, UsageOnly: true}).DetectApps(t.Context())
			if (err != nil) != tc.fail || len(apps) != tc.want {
				t.Fatalf("apps=%v err=%v", apps, err)
			}
			if tc.want == 1 && (apps[0].Name != "Example Editor" || apps[0].Path != "") {
				t.Fatalf("expected only a name: %+v", apps[0])
			}
		})
	}
}
