package personalassistant

import (
	"strings"
	"testing"
	"time"
)

func samplePlanLines() []FolderPlanLine {
	return []FolderPlanLine{
		{Kind: FolderPlanIntegration, Name: "Installs the reviewed REAPER integration 0.9.0"},
		{Kind: FolderPlanWorkspace, Name: "Creates a REAPER Song workspace named My Song"},
		{Kind: FolderPlanTask, Name: "Queues a first read-only task", Detail: "Starts when you press Start first look"},
	}
}

func TestFolderPlanDigestIgnoresState(t *testing.T) {
	base := FolderPlanDigest(samplePlanLines())
	if base == "" {
		t.Fatal("digest is empty")
	}
	progressed := samplePlanLines()
	progressed[0].State = FolderLineDone
	progressed[1].State = FolderLineWorking
	if got := FolderPlanDigest(progressed); got != base {
		t.Fatalf("state changed the digest: %s != %s", got, base)
	}
}

func TestFolderPlanDigestIgnoresProgressAndTiming(t *testing.T) {
	base := FolderPlanDigest(samplePlanLines())
	live := samplePlanLines()
	started := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	live[0].State = FolderLineDone
	live[0].Progress = "Installed Ori REAPER Plugin 0.9.0"
	live[0].StartedAt, live[0].FinishedAt = started, started.Add(6*time.Second)
	live[1].State = FolderLineWorking
	live[1].Progress = "Downloading Ori REAPER Plugin 0.9.0"
	live[1].StartedAt = started.Add(6 * time.Second)
	if got := FolderPlanDigest(live); got != base {
		t.Fatalf("progress or timing changed the digest: %s != %s", got, base)
	}
}

func TestFolderPlanLineMark_StampsTimesAndWords(t *testing.T) {
	t0 := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	line := FolderPlanLine{Kind: FolderPlanIntegration, Name: "Installs REAPER tools"}
	line.Mark(FolderLineWorking, "Checking the reviewed release", t0)
	line.Mark(FolderLineWorking, "Downloading Ori REAPER Plugin 0.9.0", t0.Add(2*time.Second))
	if line.StartedAt != t0 || !line.FinishedAt.IsZero() || line.Progress != "Downloading Ori REAPER Plugin 0.9.0" {
		t.Fatalf("working line = %+v", line)
	}
	line.Mark(FolderLineWorking, "/Users/someone/Music", t0.Add(3*time.Second))
	if line.Progress != "Downloading Ori REAPER Plugin 0.9.0" {
		t.Fatalf("a path-shaped progress was stored: %q", line.Progress)
	}
	line.Mark(FolderLineDone, "Installed Ori REAPER Plugin 0.9.0", t0.Add(6*time.Second))
	if line.StartedAt != t0 || line.FinishedAt != t0.Add(6*time.Second) || line.Progress != "Installed Ori REAPER Plugin 0.9.0" {
		t.Fatalf("done line = %+v", line)
	}

	quiet := FolderPlanLine{Kind: FolderPlanWorkspace, Name: "Connects"}
	quiet.Mark(FolderLineWorking, "Enabling it in One Song", t0)
	quiet.Mark(FolderLineDone, "", t0.Add(time.Second))
	if quiet.Progress != "" {
		t.Fatalf("a done line without words kept its working words: %q", quiet.Progress)
	}

	failed := FolderPlanLine{Kind: FolderPlanWorkspace, Name: "Connects"}
	failed.Mark(FolderLineWorking, "Enabling it in One Song", t0)
	failed.Mark(FolderLineFailed, "", t0.Add(time.Second))
	if failed.Progress != "Enabling it in One Song" || failed.FinishedAt != t0.Add(time.Second) {
		t.Fatalf("failed line = %+v", failed)
	}

	instant := FolderPlanLine{Kind: FolderPlanFolder, Name: "Nothing in Albums is moved, copied or read"}
	instant.Mark(FolderLineDone, "", t0)
	if instant.StartedAt != t0 || instant.FinishedAt != t0 {
		t.Fatalf("a line done without working = %+v", instant)
	}
}

func TestValidateFolderPlanLineBoundsProgress(t *testing.T) {
	ok := FolderPlanLine{Kind: FolderPlanIntegration, Name: "Installs Ori REAPER Plugin", Progress: "Downloading Ori REAPER Plugin 0.9.0"}
	if err := ValidateFolderPlanLine(ok); err != nil {
		t.Fatalf("plain progress refused: %v", err)
	}
	if err := validateFolderSetupRun(FolderSetupRun{Status: FolderSetupRunning, Lines: []FolderPlanLine{ok}}); err != nil {
		t.Fatalf("stored run with plain progress refused: %v", err)
	}
	for name, progress := range map[string]string{
		"too long": strings.Repeat("a", folderSetupMaxText+1),
		"newline":  "Downloading\nthe plugin",
		"nul":      "Downloading\x00",
		"path":     "/Users/someone/Music/Albums",
		"home":     "~/Music/Albums",
	} {
		bad := ok
		bad.Progress = progress
		if err := ValidateFolderPlanLine(bad); err == nil {
			t.Errorf("%s: progress %q accepted", name, progress)
		}
		run := FolderSetupRun{Status: FolderSetupRunning, Lines: []FolderPlanLine{bad}}
		if err := validateFolderSetupRun(run); err == nil {
			t.Errorf("%s: stored run with progress %q accepted", name, progress)
		}
	}
}

func TestFolderPlanDigestChangesWithAnyLine(t *testing.T) {
	base := FolderPlanDigest(samplePlanLines())
	cases := map[string]func([]FolderPlanLine) []FolderPlanLine{
		"reordered": func(l []FolderPlanLine) []FolderPlanLine { l[0], l[1] = l[1], l[0]; return l },
		"renamed":   func(l []FolderPlanLine) []FolderPlanLine { l[1].Name = "Creates another workspace"; return l },
		"detail":    func(l []FolderPlanLine) []FolderPlanLine { l[2].Detail = ""; return l },
		"kind":      func(l []FolderPlanLine) []FolderPlanLine { l[0].Kind = FolderPlanHome; return l },
		"removed":   func(l []FolderPlanLine) []FolderPlanLine { return l[:2] },
		"added": func(l []FolderPlanLine) []FolderPlanLine {
			return append(l, FolderPlanLine{Kind: FolderPlanMode, Name: "Uses File-only mode"})
		},
	}
	for name, mutate := range cases {
		if got := FolderPlanDigest(mutate(samplePlanLines())); got == base {
			t.Errorf("%s did not change the digest", name)
		}
	}
}

func TestNewFolderSetupPlanStampsDigestAndCopies(t *testing.T) {
	lines := samplePlanLines()
	plan := NewFolderSetupPlan(lines)
	if plan.Digest != FolderPlanDigest(lines) {
		t.Fatal("plan digest does not match its lines")
	}
	lines[0].Name = "mutated after build"
	if plan.Lines[0].Name == "mutated after build" {
		t.Fatal("plan aliases the caller's slice")
	}
}

// The songs a card promised to open are part of what it was confirmed for,
// whether or not anything else of the intent is bound.
func TestFolderPlanDigestBindsTheProjectsItOpens(t *testing.T) {
	stamp := func(policy string, opens ...string) string {
		plan := NewFolderSetupPlan(samplePlanLines())
		plan.Intent = FolderSetupIntent{Portfolio: true, OptionalTaskPolicy: policy, OpensProjects: opens}
		return plan.Stamped().Digest
	}
	for _, policy := range []string{"", FolderOptionalSummaryPolicy} {
		none, five, six := stamp(policy), stamp(policy, "Album-5"), stamp(policy, "Album-6")
		if none == "" || five == "" || six == "" || none == five || five == six {
			t.Fatalf("policy %q: none=%s five=%s six=%s", policy, none, five, six)
		}
		if again := stamp(policy, "Album-5"); again != five {
			t.Fatalf("policy %q: the same promise stamped differently", policy)
		}
	}
	for _, bad := range [][]string{{"Albums/Album-5"}, {"../Album-5"}, {" Album-5"}, {""}, {"Album-5", "Album-5"}, {`C:\Album-5`}} {
		if digest := stamp("", bad...); digest != "" {
			t.Errorf("%q is no plain folder name, yet the plan can be confirmed", bad)
		}
	}
}
