package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestReadyShowsOnlyTheReadyColumnInRankOrder(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{items: mixedBoard}, "ready")

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr.String())
	}

	output := stdout.String()
	for _, leaked := range []string{"Skin Makeover", "Imported Workspace"} {
		if strings.Contains(output, leaked) {
			t.Fatalf("a Backlog card leaked into the Ready listing: %q", output)
		}
	}
	if !strings.Contains(output, "3 ready") {
		t.Fatalf("listing = %q, want the Ready count", output)
	}
	// The whole point of the command is the order, so it is asserted as an
	// order rather than as three independent presence checks.
	ranked := indexOfAll(output, "#292", "[draft]", "#297")
	if !ascending(ranked) {
		t.Fatalf("listing = %q, want rank 1, then rank 2, then the unranked card last", output)
	}
	for _, want := range []string{"[M]", "builds on the map work that just shipped", "Ori Dev"} {
		if !strings.Contains(output, want) {
			t.Fatalf("listing = %q, want it to contain %q", output, want)
		}
	}
}

// TestReadyRendersAnUnrankedCardAsADashNotAZero guards a small lie: 0 reads
// like a rank somebody chose, when it means nobody has placed the card yet.
func TestReadyRendersAnUnrankedCardAsADashNotAZero(t *testing.T) {
	t.Parallel()
	application, stdout, _, base := newBoardApp(t, &fakeBoard{items: mixedBoard}, "ready")

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		if strings.Contains(line, "#297") {
			if !strings.HasPrefix(strings.TrimSpace(line), "-") {
				t.Fatalf("unranked row = %q, want it to lead with a dash", line)
			}
			if strings.Contains(line, "0 ") {
				t.Fatalf("unranked row = %q, want no invented rank", line)
			}
		}
	}
}

func TestReadySaysNothingIsGroomedWithoutSoundingBroken(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{
		items: `{"items":[{"title":"Skin Makeover","status":"Backlog",
			"content":{"number":293,"type":"Issue","url":"u"}}],"totalCount":1}`,
	}, "ready")

	// A board with nothing groomed is a successful answer, so it exits 0 — a
	// script must not treat "nothing ready today" as a failure.
	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0 for an empty Ready column; stderr=%q", exit, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "nothing in Ready") {
		t.Fatalf("listing = %q, want it to say the board was read and nothing is groomed", output)
	}
	// The reader is sent to the column the ungroomed captures are actually
	// sitting in, so an empty Ready column is a signpost rather than a dead end.
	if !strings.Contains(output, "./scripts/devops/backlog.sh") {
		t.Fatalf("listing = %q, want it to point at the Backlog column", output)
	}
}

func TestReadyJSONIsItsOwnVersionedEnvelope(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{items: mixedBoard}, "ready")

	if exit := application.Run(context.Background(), append(base, "--json")); exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", exit, stderr.String())
	}

	var payload boardPayload
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("stdout = %q is not the envelope: %v", stdout.String(), err)
	}
	if payload.SchemaVersion != readySchemaVersion || payload.Repository != "johnjallday/ori-agent" {
		t.Fatalf("payload = %+v, want the schema-versioned envelope", payload)
	}
	// The payload names its own column, so a consumer holding one of the two
	// identical shapes never has to infer which command produced it.
	if payload.Column != "Ready" {
		t.Fatalf("column = %q, want the payload to name the column it read", payload.Column)
	}
	if payload.Project.Number != 3 || payload.Project.Title != "Ori Dev" || payload.Project.URL == "" {
		t.Fatalf("project = %+v, want the board it actually read named", payload.Project)
	}
	if payload.ObservedAt == "" || !payload.Complete {
		t.Fatalf("payload = %+v, want an observation time and a complete listing", payload)
	}
	if len(payload.Items) != 3 {
		t.Fatalf("items = %d, want only the Ready column", len(payload.Items))
	}

	// An absent rank is encoded as absent. Zero is a rank somebody could have
	// chosen, so a consumer must be able to tell the two apart.
	last := payload.Items[2]
	if last.Rank != nil {
		t.Fatalf("unranked item carried rank %v", *last.Rank)
	}
	if first := payload.Items[0]; first.Rank == nil || *first.Rank != 1 {
		t.Fatalf("first item = %+v, want rank 1", first)
	}
	// A draft has no Issue behind it, so it carries no number to go looking for.
	draft := payload.Items[1]
	if !draft.IsDraft || draft.Number != nil {
		t.Fatalf("draft = %+v, want is_draft with no Issue number", draft)
	}
	// Human prose belongs to the human surface, not to the machine one.
	if strings.Contains(stdout.String(), "Ori ready") {
		t.Fatalf("JSON output contained the human header: %s", stdout.String())
	}
}

func TestReadyJSONEncodesAnEmptyColumnAsAnArray(t *testing.T) {
	t.Parallel()
	application, stdout, _, base := newBoardApp(t, &fakeBoard{}, "ready")

	if exit := application.Run(context.Background(), append(base, "--json")); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	// A consumer iterating a JSON array must not have to special-case null for
	// the most ordinary state this command has.
	if !strings.Contains(stdout.String(), `"items": []`) &&
		!strings.Contains(stdout.String(), `"items":[]`) {
		t.Fatalf("empty board = %s, want an empty items array", stdout.String())
	}
}

func TestReadyOutputIsPlainTextWhenItIsNotATerminal(t *testing.T) {
	t.Parallel()
	application, stdout, _, base := newBoardApp(t, &fakeBoard{items: mixedBoard}, "ready")

	if exit := application.Run(context.Background(), base); exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if strings.ContainsRune(stdout.String(), '\x1b') {
		t.Fatalf("redirected output carried escape sequences: %q", stdout.String())
	}
}

func TestReadyFailsLoudlyRatherThanShowingAnEmptyColumn(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{failure: errNoNetwork{}}, "ready")

	exit := application.Run(context.Background(), base)

	if exit != 1 {
		t.Fatalf("exit = %d, want 1 when GitHub cannot answer", exit)
	}
	// The dangerous failure mode is a reachable-looking empty column, because
	// "nothing to do today" is a plausible answer that happens to be false.
	if strings.Contains(stdout.String(), "nothing in Ready") {
		t.Fatalf("a failed read was rendered as an empty column: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Recovery:") {
		t.Fatalf("stderr = %q, want a recovery action", stderr.String())
	}
}

// TestReadyRefusesWhenSeveralBoardsAreLinked pins the behaviour at the
// repository level: no guess, and both candidates named.
func TestReadyRefusesWhenSeveralBoardsAreLinked(t *testing.T) {
	t.Parallel()
	application, stdout, stderr, base := newBoardApp(t, &fakeBoard{
		projects: `{"data":{"repository":{"projectsV2":{"nodes":[
			{"number":3,"title":"Ori Dev","url":"a","owner":{"login":"johnjallday"}},
			{"number":4,"title":"Ori Dev","url":"b","owner":{"login":"johnjallday"}}]}}}}`,
	}, "ready")

	if exit := application.Run(context.Background(), base); exit != 1 {
		t.Fatalf("exit = %d, want 1 when the board is ambiguous", exit)
	}
	if stdout.Len() != 0 {
		t.Fatalf("an ambiguous board still printed a listing: %q", stdout.String())
	}
	for _, want := range []string{"#3", "#4", "unlink"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), want)
		}
	}
}

// TestReadyPointsAtBacklogRatherThanClaimingItIsNotACommand covers the reverse
// of the confusion this split exists to fix: someone reaching for the other
// column from the wrong command gets sent there, not told they made it up.
func TestReadyPointsAtBacklogRatherThanClaimingItIsNotACommand(t *testing.T) {
	t.Parallel()
	remote := &fakeBoard{items: mixedBoard}
	application, _, stderr, base := newBoardApp(t, remote, "ready")

	if exit := application.Run(context.Background(), append(base, "backlog")); exit != 2 {
		t.Fatalf("exit = %d, want 2", exit)
	}
	if !strings.Contains(stderr.String(), "./scripts/devops/backlog.sh") {
		t.Fatalf("stderr = %q, want it to name the Backlog command", stderr.String())
	}
	if len(remote.calls) != 0 {
		t.Fatalf("a rejected invocation still queried GitHub: %v", remote.calls)
	}
}
