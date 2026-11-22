//go:build linux

package detector

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecentUsageRequiresDatedApplicationEvidence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := NewLinuxDetector(Config{RecencyDays: 7, UsageOnly: true})
	path := filepath.Join(home, ".local", "share", "recently-used.xbel")
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DetectApps(t.Context()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing evidence = %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, tc := range []struct {
		name, xml string
		failed    bool
		count     int
	}{{"corrupt", "not xml", true, 0}, {"empty", "<xbel/>", false, 0}, {"unknown use", `<xbel><bookmark modified="` + now + `"><info><metadata><applications><application name="Example"/></applications></metadata></info></bookmark></xbel>`, true, 0}, {"recent app not old app", `<xbel><bookmark modified="` + now + `"><info><metadata><applications><application name="Example" modified="` + now + `"/><application name="Old" modified="2000-01-01T00:00:00Z"/></applications></metadata></info></bookmark></xbel>`, false, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.xml), 0600); err != nil {
				t.Fatal(err)
			}
			apps, err := d.DetectApps(t.Context())
			if (err != nil) != tc.failed || len(apps) != tc.count {
				t.Fatalf("got %d names, err=%v", len(apps), err)
			}
		})
	}
}
