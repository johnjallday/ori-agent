package filejanitor

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func groupFixture(names ...string) (JanitorBatch, []JanitorCandidate) {
	batch := JanitorBatch{ID: "batch-1", WorkspaceID: "ws-1", RootID: "root-1"}
	candidates := make([]JanitorCandidate, 0, len(names))
	for i, name := range names {
		candidate := JanitorCandidate{
			ID: fmt.Sprintf("cand-%d", i), WorkspaceID: batch.WorkspaceID, BatchID: batch.ID,
			Name: name, State: CandidatePending,
			Fingerprint: Fingerprint{Name: name, Size: 10, ModTime: time.Unix(100, 0)},
		}
		ClassifyMetadata(candidate).Apply(&candidate)
		batch.CandidateIDs = append(batch.CandidateIDs, candidate.ID)
		candidates = append(candidates, candidate)
	}
	return batch, candidates
}

func mustGroup(t *testing.T, batch JanitorBatch, candidates []JanitorCandidate) ReviewGroups {
	t.Helper()
	got, err := GroupReview(batch, candidates, "validated-scope")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func groupFor(t *testing.T, groups ReviewGroups, candidateID string) (ReviewGroup, ReviewGroupMember) {
	t.Helper()
	for _, group := range groups.Groups {
		members, _ := groups.Members(group.ID)
		for _, member := range members {
			if member.Candidate.ID == candidateID {
				return group, member
			}
		}
	}
	t.Fatalf("candidate %s unreachable", candidateID)
	return ReviewGroup{}, ReviewGroupMember{}
}

func TestReviewGroups_ExclusiveAccountingAndPrecedence(t *testing.T) {
	batch, candidates := groupFixture("tax-v2.wav", "tax.wav", "track.wav", "unknown.zzzz", "photo.png", "app.dmg", "raw.xyz", "kept.pdf", "done.pdf", "stale.pdf", "failed.pdf", "applying.pdf", "bad.pdf")
	candidates[0].NeedsReview = true // all four cautions overlap, none disappears
	candidates[6].Category, candidates[6].NeedsReview, candidates[6].Confidence = "unsupported", false, ConfidenceHigh
	for i, state := range []CandidateState{CandidateSkipped, CandidateApplied, CandidateStale, CandidateFailed, CandidateApplying, "future"} {
		candidates[7+i].State = state
	}
	batch.Ineligible = []IneligibleObservation{{Name: "folder", Reason: IneligibleNotRegularFile}}
	got := mustGroup(t, batch, candidates)
	want := ReviewGroupCounts{Candidates: 13, Filing: 2, Questions: 5, Kept: 1, Applied: 1, Stale: 1, Failed: 1, Applying: 1, Unknown: 1, Ineligible: 1}
	if got.Counts != want {
		t.Fatalf("counts = %+v, want %+v", got.Counts, want)
	}
	group, member := groupFor(t, got, candidates[0].ID)
	if group.ID != reviewGroupID("sensitive") || !reflect.DeepEqual(member.Cautions, []string{"sensitive", "versions", "audio", "unresolved"}) {
		t.Fatalf("precedence lost evidence: %+v %+v", group, member)
	}
	group, _ = groupFor(t, got, candidates[6].ID)
	if group.ID != reviewGroupID("unmatched") {
		t.Fatal("unsupported candidate must remain reachable")
	}
	seen := map[string]bool{}
	for _, group := range got.Groups {
		members, found := got.Members(group.ID)
		if !found || len(members) != group.Count {
			t.Fatal("card and member counts disagree")
		}
		for _, member := range members {
			if seen[member.Candidate.ID] {
				t.Fatal("double-counted candidate")
			}
			seen[member.Candidate.ID] = true
		}
	}
	if len(seen) != got.Counts.Filing+got.Counts.Questions {
		t.Fatal("pending accounting mismatch")
	}
}

func TestReviewGroups_ConservativeNameRules(t *testing.T) {
	for _, tc := range []struct {
		name, partner, key string
	}{
		{"taxi.pdf", "", "filing-documents"},
		{"secretary.pdf", "", "filing-documents"},
		{"tax-return.pdf", "", "sensitive"},
		{"PASSWORDS.csv", "", "sensitive"},
		{"report-v2.pdf", "", "filing-documents"}, // no corroborating saved name
		{"report-v2.pdf", "report.pdf", "versions"},
		{"report (2).pdf", "report.pdf", "versions"},
		{"report copy.pdf", "report.pdf", "versions"},
		{"report-version 2.pdf", "report-v1.pdf", "versions"},
		{"report-v2.pdf", "report.png", "filing-documents"}, // different type
		{"photo-001.png", "photo-002.png", "filing-images"},
		{"PHOTO.png", "photo.png", "filing-images"}, // case alone is not copy evidence
		{"素材-v2.pdf", "素材.pdf", "versions"},
		{"song.wav", "", "audio"},
		{"song.rpp", "", "audio"},
		{"bundle.zip", "bundle.pdf", "filing-archives"}, // not an expendable duplicate
		{"unknown.zzzzz", "", "unresolved"},
	} {
		t.Run(tc.name+tc.partner, func(t *testing.T) {
			names := []string{tc.name}
			if tc.partner != "" {
				names = append(names, tc.partner)
			}
			batch, candidates := groupFixture(names...)
			group, _ := groupFor(t, mustGroup(t, batch, candidates), candidates[0].ID)
			if group.ID != reviewGroupID(tc.key) {
				t.Fatalf("group = %s, want %s", group.Label, tc.key)
			}
		})
	}
}

func TestReviewGroups_CategoryAliasesRemainBoundedWithoutInventingGroups(t *testing.T) {
	batch, candidates := groupFixture("one.png", "two.wav", "three.zzz")
	candidates[0].Category = " IMAGES "
	candidates[1].Category = "AUDIO"
	candidates[2].Category, candidates[2].NeedsReview, candidates[2].Confidence = "Other", false, ConfidenceHigh
	got := mustGroup(t, batch, candidates)
	for i, key := range []string{"filing-images", "audio", "unresolved"} {
		group, _ := groupFor(t, got, candidates[i].ID)
		if group.ID != reviewGroupID(key) {
			t.Fatalf("category alias created a missing/invented group: %+v", group)
		}
	}
}

func TestReviewGroups_StableMembershipUnaffectedByDestination(t *testing.T) {
	batch, candidates := groupFixture("report.pdf", "photo.png", "app.dmg")
	before := mustGroup(t, batch, candidates)
	candidates[0].Decision, candidates[0].DecisionCategory = DecisionMove, CategoryImages
	candidates[0].DecidedAt = time.Now()
	// Canonical batch order wins even when storage candidate order differs.
	candidates[0], candidates[2] = candidates[2], candidates[0]
	after := mustGroup(t, batch, candidates)
	if before.Revision != after.Revision || !reflect.DeepEqual(before.Groups, after.Groups) {
		t.Fatal("a destination edit changed proposal membership")
	}
	if before.Groups[0].ID != reviewGroupID("filing-documents") || before.Groups[0].Destination != "Filed/Documents" {
		t.Fatalf("category registry ordering/destination lost: %+v", before.Groups)
	}
	// Fingerprint, classification, scope and lifecycle changes must all fence
	// the displayed snapshot, even when the card label happens to be unchanged.
	for _, change := range []func(*JanitorCandidate){
		func(c *JanitorCandidate) { c.Fingerprint.FileID = "replacement" },
		func(c *JanitorCandidate) { c.NeedsReview = true },
		func(c *JanitorCandidate) { c.Category = CategoryImages },
		func(c *JanitorCandidate) { c.State = CandidateSkipped },
	} {
		copy := append([]JanitorCandidate(nil), candidates...)
		change(&copy[0])
		if mustGroup(t, batch, copy).Revision == before.Revision {
			t.Fatal("relevant evidence change did not change revision")
		}
	}
	other, err := GroupReview(batch, candidates, "different-scope")
	if err != nil || other.Revision == before.Revision {
		t.Fatal("scope must bind revision")
	}
}

func TestReviewGroups_BoundedCardsSamplesAndHostileNames(t *testing.T) {
	for _, size := range []int{0, 1, 50, 51, 200, 206, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			names := make([]string, size)
			for i := range names {
				names[i] = fmt.Sprintf("image-%04d.png", i)
			}
			batch, candidates := groupFixture(names...)
			got := mustGroup(t, batch, candidates)
			if got.Counts.Candidates != size || got.Counts.Filing != size || len(got.Groups) > 1 {
				t.Fatalf("lost full counts: %+v", got)
			}
			if size > 0 && (got.Groups[0].Count != size || len(got.Groups[0].Samples) != min(size, ReviewGroupSampleLimit)) {
				t.Fatal("sample/page confused with whole membership")
			}
			data, err := json.Marshal(got)
			if err != nil || len(data) > 2000 || strings.Contains(string(data), "cand-") || strings.Contains(string(data), "fingerprint") {
				t.Fatalf("card payload unbounded or leaks member IDs: %d bytes, %v", len(data), err)
			}
		})
	}
	batch, candidates := groupFixture("<img onerror=alert(1)>.png", "invoice\u202egpj.exe", "a\nlog.pdf", strings.Repeat("界", 200)+".pdf")
	got := mustGroup(t, batch, candidates)
	for _, group := range got.Groups {
		for _, name := range group.Samples {
			if strings.ContainsAny(name, "\n\u202e") || len([]rune(name)) > maxDisplayNameRunes+1 {
				t.Fatalf("unsafe/unbounded sample: %q", name)
			}
		}
	}
	// HTML is data, not stripped or parsed as a command. JSON escapes it and
	// the browser renderer must use textContent, never innerHTML.
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "<img") {
		t.Fatal("HTML sample was not JSON-escaped")
	}
}

func TestReviewGroups_RejectIncompleteOrAmbiguousBatch(t *testing.T) {
	for _, mode := range []string{"missing", "duplicate", "foreign workspace", "foreign batch", "duplicate reference"} {
		t.Run(mode, func(t *testing.T) {
			batch, candidates := groupFixture("one.pdf", "two.pdf")
			switch mode {
			case "missing":
				candidates = candidates[:1]
			case "duplicate":
				candidates[1].ID = candidates[0].ID
			case "foreign workspace":
				candidates[0].WorkspaceID = "ws-2"
			case "foreign batch":
				candidates[0].BatchID = "other"
			case "duplicate reference":
				batch.CandidateIDs[1] = batch.CandidateIDs[0]
			}
			if _, err := GroupReview(batch, candidates, "scope"); err == nil {
				t.Fatal("ambiguous membership must fail closed")
			}
		})
	}
}

func TestReviewGroups_PureProjectionDoesNotChangeInputs(t *testing.T) {
	batch, candidates := groupFixture("one.pdf", "two.pdf")
	before, _ := json.Marshal([]any{batch, candidates})
	got := mustGroup(t, batch, candidates)
	members, _ := got.Members(got.Groups[0].ID)
	members[0].Candidate.Name = "changed.pdf"
	after, _ := json.Marshal([]any{batch, candidates})
	if string(before) != string(after) {
		t.Fatal("projection changed canonical input")
	}
	again, _ := got.Members(got.Groups[0].ID)
	if again[0].Candidate.Name != "one.pdf" {
		t.Fatal("member copy mutated projection")
	}
}

func BenchmarkReviewGroups(b *testing.B) {
	names := make([]string, 1000)
	for i := range names {
		names[i] = fmt.Sprintf("image-%04d.png", i)
	}
	batch, candidates := groupFixture(names...)
	b.ResetTimer()
	for b.Loop() {
		if _, err := GroupReview(batch, candidates, "scope"); err != nil {
			b.Fatal(err)
		}
	}
}
