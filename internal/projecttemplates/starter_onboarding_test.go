package projecttemplates_test

import (
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/templateonboarding"
)

func TestReaperStarterOnboardingSpecValid(t *testing.T) {
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := projecttemplates.EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	tpl, err := projecttemplates.FindLibraryTemplate(libDir, "reaper-song")
	if err != nil {
		t.Fatalf("FindLibraryTemplate: %v", err)
	}
	spec, err := templateonboarding.ParseSpec(tpl.Onboarding)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if res := templateonboarding.Validate(spec); !res.OK() {
		t.Fatalf("Validate: %v", res.Err())
	}
	if len(spec.Fields) != 3 || spec.Fields[0].ID != "bpm" || spec.Fields[1].ID != "key" || spec.Fields[2].ID != "song_name" {
		t.Fatalf("unexpected reaper onboarding fields: %+v", spec.Fields)
	}
	if spec.Completion.Type != templateonboarding.ActionTask || !spec.Completion.InstantiateSkeleton {
		t.Fatalf("completion = %+v, want task with skeleton instantiation", spec.Completion)
	}
	if len(spec.Completion.SkillRefs) != 1 || spec.Completion.SkillRefs[0] != "reaper-session-setup" {
		t.Fatalf("skill refs = %#v, want reaper-session-setup", spec.Completion.SkillRefs)
	}
}
