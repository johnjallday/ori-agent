package blueprintintake

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func validRequirement() workspace.IntakeRequirement {
	return workspace.IntakeRequirement{
		Key:   " Course-Materials ",
		Label: " Course materials ",
		Skill: "Syllabus Intake",
		Sources: workspace.IntakeSources{
			Files:        true,
			URL:          true,
			DirectoryKey: " Course-Root ",
		},
		AcceptedExtensions: []string{"PDF", ".md", "pdf"},
		ProposalKinds:      []string{"Ticket", "memory", "ticket"},
	}
}

func TestNormalizeRequirements_NormalizesValidDeclaration(t *testing.T) {
	got, err := NormalizeRequirements(
		[]workspace.IntakeRequirement{validRequirement()},
		[]string{"Syllabus Intake"},
		[]workspace.DirectoryRequirement{{Key: "course-root"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []workspace.IntakeRequirement{{
		Key:   "course-materials",
		Label: "Course materials",
		Skill: "Syllabus Intake",
		Sources: workspace.IntakeSources{
			Files:        true,
			URL:          true,
			DirectoryKey: "course-root",
		},
		AcceptedExtensions: []string{".pdf", ".md"},
		ProposalKinds:      []string{"ticket", "memory"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeRequirements() = %#v, want %#v", got, want)
	}
}

func TestNormalizeRequirements_RejectsInvalidDeclarations(t *testing.T) {
	tests := []struct {
		name         string
		requirements func() []workspace.IntakeRequirement
		skills       []string
		directories  []workspace.DirectoryRequirement
	}{
		{
			name: "more than four intakes",
			requirements: func() []workspace.IntakeRequirement {
				out := make([]workspace.IntakeRequirement, 5)
				for i := range out {
					out[i] = validRequirement()
					out[i].Key = string(rune('a' + i))
					out[i].Sources.DirectoryKey = ""
				}
				return out
			},
		},
		{
			name: "blank key",
			requirements: func() []workspace.IntakeRequirement {
				req := validRequirement()
				req.Key = " "
				return []workspace.IntakeRequirement{req}
			},
		},
		{
			name: "duplicate normalized key",
			requirements: func() []workspace.IntakeRequirement {
				first := validRequirement()
				first.Sources.DirectoryKey = ""
				second := first
				second.Key = "course-materials"
				return []workspace.IntakeRequirement{first, second}
			},
		},
		{
			name: "blank label",
			requirements: func() []workspace.IntakeRequirement {
				req := validRequirement()
				req.Label = " "
				return []workspace.IntakeRequirement{req}
			},
		},
		{
			name: "skill not declared",
			requirements: func() []workspace.IntakeRequirement {
				req := validRequirement()
				req.Skill = "Other"
				return []workspace.IntakeRequirement{req}
			},
		},
		{
			name: "no source enabled",
			requirements: func() []workspace.IntakeRequirement {
				req := validRequirement()
				req.Sources = workspace.IntakeSources{}
				return []workspace.IntakeRequirement{req}
			},
		},
		{
			name: "directory not declared",
			requirements: func() []workspace.IntakeRequirement {
				req := validRequirement()
				req.Sources.DirectoryKey = "other"
				return []workspace.IntakeRequirement{req}
			},
		},
		{
			name: "directory used by two intakes",
			requirements: func() []workspace.IntakeRequirement {
				first := validRequirement()
				second := validRequirement()
				second.Key = "second"
				return []workspace.IntakeRequirement{first, second}
			},
		},
		{
			name: "unsupported extension",
			requirements: func() []workspace.IntakeRequirement {
				req := validRequirement()
				req.AcceptedExtensions = []string{".exe"}
				return []workspace.IntakeRequirement{req}
			},
		},
		{
			name: "no proposal kind",
			requirements: func() []workspace.IntakeRequirement {
				req := validRequirement()
				req.ProposalKinds = nil
				return []workspace.IntakeRequirement{req}
			},
		},
		{
			name: "unsupported proposal kind",
			requirements: func() []workspace.IntakeRequirement {
				req := validRequirement()
				req.ProposalKinds = []string{"command"}
				return []workspace.IntakeRequirement{req}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			skills := test.skills
			if skills == nil {
				skills = []string{"Syllabus Intake"}
			}
			directories := test.directories
			if directories == nil {
				directories = []workspace.DirectoryRequirement{{Key: "course-root"}}
			}
			_, err := NormalizeRequirements(test.requirements(), skills, directories)
			if !errors.Is(err, ErrInvalidRequirements) {
				t.Fatalf("NormalizeRequirements() error = %v, want ErrInvalidRequirements", err)
			}
		})
	}
}

func TestNormalizeRequirements_AllowsNoIntakeBlock(t *testing.T) {
	got, err := NormalizeRequirements(nil, nil, nil)
	if err != nil || got != nil {
		t.Fatalf("NormalizeRequirements(nil) = %#v, %v", got, err)
	}
}

func TestParseRequirements_RejectsUnknownFields(t *testing.T) {
	raw := json.RawMessage(`[{"key":"course","label":"Course","skill":"syllabus","sources":{"files":true,"command":"run"},"proposal_kinds":["ticket"]}]`)
	_, err := ParseRequirements(raw, []string{"syllabus"}, nil)
	if !errors.Is(err, ErrInvalidRequirements) {
		t.Fatalf("ParseRequirements() error = %v, want ErrInvalidRequirements", err)
	}
}
