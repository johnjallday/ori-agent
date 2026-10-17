package backuprecovery

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestExternalSetupGuideIsPinnedModelFreeAndNonExecuting(t *testing.T) {
	guide := ExternalSetupGuide()
	if guide.Provider != "restic" || guide.Version != "0.19.1" || len(guide.Steps) != 8 || len(guide.Sources) != 4 {
		t.Fatal("unsupported/unbounded guide")
	}
	encoded, err := json.Marshal(guide)
	if err != nil {
		t.Fatal(err)
	}
	copy := string(encoded)
	for _, required := range []string{"does not prove a backup exists", "25D771280a", "repository format 2", "separate from the drive", "workspace-scoped Vault secret reference", "Manual", "not off-site protection", "latest-attempt outcome stay unknown", "user-reported result", "Ori does not restore"} {
		if !strings.Contains(strings.ToLower(copy), strings.ToLower(required)) {
			t.Fatal("guide lost safety distinction", required)
		}
	}
	for _, forbidden := range []string{"restic init", "restic backup", "restic restore", "restic prune", "curl ", "brew install", "\"command\":", "\"executable\":", "\"password\":"} {
		if strings.Contains(copy, forbidden) {
			t.Fatal("guide contains executable/secret payload", forbidden)
		}
	}
	allowed := map[string]bool{"020_installation.rst": true, "030_preparing_a_new_repo.rst": true, "040_backup.rst": true, "050_restore.rst": true}
	for _, link := range guide.Sources {
		parsed, err := url.Parse(link.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/restic/restic/blob/v0.19.1/doc/") || !allowed[strings.TrimPrefix(parsed.Path, "/restic/restic/blob/v0.19.1/doc/")] {
			t.Fatal("unreviewed source link", link.URL)
		}
	}
	guide.Steps[0].Instructions[0] = "invented mutation"
	guide.Sources[0].URL = "https://untrusted.invalid"
	fresh := ExternalSetupGuide()
	if fresh.Steps[0].Instructions[0] == "invented mutation" || fresh.Sources[0].URL == "https://untrusted.invalid" {
		t.Fatal("guide shares mutable data")
	}
}
