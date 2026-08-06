package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestBacklogShowsTheColumnGitHubLabelsBacklog is the whole reason this command
// changed hands. It used to read Ready while being named after Backlog, so the
// count it printed disagreed with the number on the board's own column header —
// and the four cards sitting in Backlog appeared in no command at all.
func TestBacklogShowsTheColumnGitHubLabelsBacklog(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{items: mixedBoard}, "backlog")

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{"#290", "#293", "Skin Makeover", "Ori Dev"} {
		if !strings.Contains(output, want) {
			t.Fatalf("listing = %q, want it to contain %q", output, want)
		}
	}
	// A Ready card appearing here would be the same bug in the other direction.
	for _, leaked := range []string{"#292", "Coordinate based map", "[draft]"} {
		if strings.Contains(output, leaked) {
			t.Fatalf("a Ready card leaked into the Backlog listing: %q", output)
		}
	}
	// The count names its column, because a bare number beside a board title
	// does not say which of that board's columns was counted.
	if !strings.Contains(output, "2 in Backlog") {
		t.Fatalf("listing = %q, want the Backlog count", output)
	}
}

// TestBacklogRanksTheColumnItReads proves the ordering is a property of the
// shared runner and not something the Ready command owned.
func TestBacklogRanksTheColumnItReads(t *testing.T) {
	t.Parallel()
	application, stdout, _, base := newBoardApp(t, &fakeBoard{items: mixedBoard}, "backlog")

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	output := stdout.String()
	// #290 carries rank 1; #293 is unranked and must sort after it.
	if ranked := indexOfAll(output, "#290", "#293"); !ascending(ranked) {
		t.Fatalf("listing = %q, want the ranked card before the unranked one", output)
	}
	for _, want := range []string{"[S]", "blocks importing at all"} {
		if !strings.Contains(output, want) {
			t.Fatalf("listing = %q, want it to carry %q", output, want)
		}
	}
}

func TestBacklogSaysAnEmptyColumnWithoutSoundingBroken(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{
		items: `{"items":[{"title":"Coordinate based map","status":"Ready",
			"content":{"number":292,"type":"Issue","url":"u"}}],"totalCount":1}`,
	}, "backlog")

	// A board with an empty Backlog column is a successful answer, so it exits
	// 0 — a script must not treat "nothing captured" as a failure.
	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0 for an empty Backlog column; stderr=%q", exit, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Backlog column is empty") {
		t.Fatalf("listing = %q, want it to say the board was read and nothing is waiting", output)
	}
	// An empty column is a signpost: the next move is capturing something.
	if !strings.Contains(output, "./scripts/devops/issue.sh add") {
		t.Fatalf("listing = %q, want it to point at Issue capture", output)
	}
}

func TestBacklogJSONNamesItsColumnAndItsOwnVersion(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{items: mixedBoard}, "backlog")

	if exit := application.Run(context.Background(), append(base, "--json")); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr.String())
	}

	var payload boardPayload
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout = %q is not the envelope: %v", stdout.String(), err)
	}
	// Version 2, because version 1 of this same command emitted Ready. A
	// consumer that branched on version 1 must be able to see the change.
	if payload.SchemaVersion != 2 {
		t.Fatalf("schema_version = %d, want 2 — the meaning of this payload changed", payload.SchemaVersion)
	}
	if payload.Column != "Backlog" {
		t.Fatalf("column = %q, want the payload to name the column it read", payload.Column)
	}
	if payload.Repository != "johnjallday/ori-agent" || payload.Project.Number != 3 {
		t.Fatalf("payload = %+v, want the board it actually read named", payload)
	}
	if len(payload.Items) != 2 {
		t.Fatalf("items = %d, want only the Backlog column", len(payload.Items))
	}
	if first := payload.Items[0]; first.Number == nil || *first.Number != 290 {
		t.Fatalf("first item = %+v, want the ranked Backlog card first", first)
	}
	if last := payload.Items[1]; last.Rank != nil {
		t.Fatalf("unranked item carried rank %v", *last.Rank)
	}
}

// TestBacklogAndReadyCannotBothClaimACard is the invariant the split has to
// hold: the two columns partition what they show, so no card can appear in both
// listings and none of the board's own Backlog cards can go missing.
func TestBacklogAndReadyCannotBothClaimACard(t *testing.T) {
	t.Parallel()
	backlogApp, backlogOut, _, backlogBase := newBoardApp(t, &fakeBoard{items: mixedBoard}, "backlog")
	readyApp, readyOut, _, readyBase := newBoardApp(t, &fakeBoard{items: mixedBoard}, "ready")

	if exit := backlogApp.Run(context.Background(), backlogBase); exit != 0 {
		t.Fatalf("backlog exit = %d, want 0", exit)
	}
	if exit := readyApp.Run(context.Background(), readyBase); exit != 0 {
		t.Fatalf("ready exit = %d, want 0", exit)
	}

	inBacklog := renderedCards(backlogOut.String())
	inReady := renderedCards(readyOut.String())

	for _, card := range []string{"#290", "#293"} {
		if !inBacklog[card] || inReady[card] {
			t.Fatalf("%s must appear in backlog only; backlog=%v ready=%v", card, inBacklog, inReady)
		}
	}
	for _, card := range []string{"#292", "#297", "[draft]"} {
		if !inReady[card] || inBacklog[card] {
			t.Fatalf("%s must appear in ready only; backlog=%v ready=%v", card, inBacklog, inReady)
		}
	}
	// Every card on the board lands in exactly one of the two listings, so the
	// split cannot quietly drop one the way the old naming dropped Backlog.
	if total := len(inBacklog) + len(inReady); total != 5 {
		t.Fatalf("the two columns rendered %d cards between them, want all 5", total)
	}
}

func TestBacklogFailsLoudlyRatherThanShowingAnEmptyBoard(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{failure: errNoNetwork{}}, "backlog")

	exit := application.Run(context.Background(), base)

	if exit != 1 {
		t.Fatalf("exit = %d, want 1 when GitHub cannot answer", exit)
	}
	// The dangerous failure mode is a reachable-looking empty backlog, because
	// "nothing to do today" is a plausible answer that happens to be false.
	if strings.Contains(stdout.String(), "Backlog column is empty") {
		t.Fatalf("a failed read was rendered as an empty column: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Recovery:") {
		t.Fatalf("stderr = %q, want a recovery action", stderr.String())
	}
}

func TestBacklogOutputIsPlainTextWhenItIsNotATerminal(t *testing.T) {
	t.Parallel()
	application, stdout, _, base := newBoardApp(t, &fakeBoard{items: mixedBoard}, "backlog")

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if strings.ContainsRune(stdout.String(), '\x1b') {
		t.Fatalf("redirected output carried escape sequences: %q", stdout.String())
	}
}

// TestBacklogRetiredSubcommandsNameTheirReplacement covers the week after a
// split, when muscle memory still types the old command.
//
// Each retired spelling was a real, working command recently, so the failure
// has to say where it went. Falling through to "unknown subcommand" would claim
// it never existed, which is both false and unhelpful.
func TestBacklogRetiredSubcommandsNameTheirReplacement(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		args []string
		want string
	}{
		"view moved":  {[]string{"view", "292"}, "./scripts/devops/issue.sh view"},
		"show moved":  {[]string{"show", "292"}, "./scripts/devops/issue.sh view"},
		"add moved":   {[]string{"add", "a title"}, "./scripts/devops/issue.sh add"},
		"new moved":   {[]string{"new", "a title"}, "./scripts/devops/issue.sh add"},
		"list moved":  {[]string{"list"}, "./scripts/devops/issue.sh"},
		"ls moved":    {[]string{"ls"}, "./scripts/devops/issue.sh"},
		"--all moved": {[]string{"--all"}, "./scripts/devops/issue.sh --all"},
		// The column this command used to read. Someone typing it is
		// remembering the old behaviour, so it is named rather than rejected.
		"ready moved": {[]string{"ready"}, "./scripts/devops/ready.sh"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			remote := &fakeBoard{items: mixedBoard}
			application, stdout, stderr, base := newBoardApp(t, remote, "backlog")

			exit := application.Run(context.Background(), append(base, testCase.args...))

			if exit != 2 {
				t.Fatalf("exit = %d, want 2 for a retired invocation; stdout=%q", exit, stdout.String())
			}
			if !strings.Contains(stderr.String(), testCase.want) {
				t.Fatalf("stderr = %q, want it to name %q", stderr.String(), testCase.want)
			}
			// A rejected invocation must cost nothing: no board read, and above
			// all no Issue created by a spelling this command no longer owns.
			if len(remote.calls) != 0 {
				t.Fatalf("a retired invocation still queried GitHub: %v", remote.calls)
			}
		})
	}
}

// TestBacklogListIsNotSilentlyRedefined is the one retirement that could have
// gone wrong quietly. `list` used to mean "list Issues" and is also the obvious
// spelling for "list the board", so accepting it would answer a different
// question than the one asked — with no error to notice.
func TestBacklogListIsNotSilentlyRedefined(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{items: mixedBoard}, "backlog")

	exit := application.Run(context.Background(), append(base, "list"))

	if exit != 2 {
		t.Fatalf("exit = %d, want 2 — `list` must not quietly become the board listing", exit)
	}
	if strings.Contains(stdout.String(), "Skin Makeover") {
		t.Fatalf("`backlog.sh list` rendered the board: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Backlog column") {
		t.Fatalf("stderr = %q, want it to explain what backlog.sh now reads", stderr.String())
	}
}
