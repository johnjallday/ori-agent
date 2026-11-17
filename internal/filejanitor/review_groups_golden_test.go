package filejanitor

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Group 4 adds a family and a bundle identifier to each member for the picture. That
// must change nothing a saved choice or a stale-view check depends on: the group
// revision and every group's membership are exactly what they were before. These
// values were captured on the build before the identifiers existed, so a change to
// either is a change to the safety contract and has to be made on purpose, with
// ReviewGroupRuleVersion bumped.
const goldenGroupRevision = "eb7390eb89ce0b6b78d9398260f6fc34ba2780dca58be0ab2401a0574650455a"

const goldenGroupMembership = `group-2087628b6d39c989a9d93f8b|question|2|cand-8,cand-9
group-32d09e8ab6c6c03a76c577f7|question|6|cand-0,cand-1,cand-11,cand-12,cand-2,cand-3
group-44276b7f4447896bff59f461|question|4|cand-4,cand-5,cand-6,cand-7
group-b200ec3f7a9cead79eb6e738|filing|2|cand-13,cand-15
group-f093a5d075235f6472ccc2ab|question|1|cand-10
group-faa511e5335255e3a92e49aa|filing|3|cand-14,cand-16,cand-17`

func goldenGroups(t *testing.T) ReviewGroups {
	t.Helper()
	batch, candidates := groupFixture(
		"Budget.pdf", "Budget (2).pdf", "Budget-v2.pdf", "Budget copy.pdf",
		"Song.wav", "Song.als", "Song 2.wav", "solo.mp3",
		"passport-a.pdf", "tax-return.pdf", "mystery.qqq",
		"photo.png", "photo (2).png", "a.dmg", "notes.txt",
		"\u202etxt.exe", "café menu.pdf", strings.Repeat("long-name-", 40)+".pdf",
	)
	return mustGroup(t, batch, candidates)
}

func membershipSignature(groups ReviewGroups) string {
	lines := make([]string, 0, len(groups.Groups))
	for _, group := range groups.Groups {
		members, _ := groups.Members(group.ID)
		ids := make([]string, 0, len(members))
		for _, member := range members {
			ids = append(ids, member.Candidate.ID)
		}
		sort.Strings(ids)
		lines = append(lines, fmt.Sprintf("%s|%s|%d|%s", group.ID, group.Kind, group.Count, strings.Join(ids, ",")))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestGroupReview_RevisionAndMembershipAreUnchangedByPairAndBundleIdentifiers(t *testing.T) {
	groups := goldenGroups(t)
	if groups.Revision != goldenGroupRevision {
		t.Fatalf("group revision changed: got %s, want %s", groups.Revision, goldenGroupRevision)
	}
	if got := membershipSignature(groups); got != goldenGroupMembership {
		t.Fatalf("group membership changed:\n%s", got)
	}
	if ReviewGroupRuleVersion != 3 {
		t.Fatalf("ReviewGroupRuleVersion is %d; the pair and bundle identifiers must not change it", ReviewGroupRuleVersion)
	}
}
