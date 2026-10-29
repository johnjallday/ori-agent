package personalassistant

import "testing"

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
