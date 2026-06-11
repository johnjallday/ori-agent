package workspace

import (
	"strings"
	"testing"
)

func TestValidateWorkspaceTagsNormalizesAndDeduplicates(t *testing.T) {
	got, err := ValidateWorkspaceTags([]string{" Music ", "music", "", "REAPER", "client:Acme"})
	if err != nil {
		t.Fatalf("ValidateWorkspaceTags: %v", err)
	}
	want := []string{"music", "reaper", "client:acme"}
	if len(got) != len(want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %#v, want %#v", got, want)
		}
	}
}

func TestValidateWorkspaceTagsRejectsLimits(t *testing.T) {
	if _, err := ValidateWorkspaceTags([]string{strings.Repeat("x", MaxWorkspaceTagLength+1)}); err == nil {
		t.Fatal("expected overlong tag to fail")
	}

	tags := make([]string, MaxWorkspaceTags+1)
	for i := range tags {
		tags[i] = "tag-" + string(rune('a'+i))
	}
	if _, err := ValidateWorkspaceTags(tags); err == nil {
		t.Fatal("expected too many tags to fail")
	}
}

func TestMergeWorkspaceTagsLenientlyCapsTemplateTags(t *testing.T) {
	additional := []string{"Music", "REAPER", strings.Repeat("x", MaxWorkspaceTagLength+1), "music"}
	got := MergeWorkspaceTags([]string{"client:acme"}, additional)
	want := []string{"client:acme", "music", "reaper"}
	if len(got) != len(want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %#v, want %#v", got, want)
		}
	}
}
