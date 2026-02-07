package skills

import "testing"

func TestValidateSkillName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{name: "good-name", wantErr: false},
		{name: "goodname123", wantErr: false},
		{name: "BadName", wantErr: true},
		{name: "bad name", wantErr: true},
		{name: "bad_name", wantErr: true},
		{name: "claude-helper", wantErr: true},
		{name: "anthropic-helper", wantErr: true},
		{name: "<tag>", wantErr: true},
	}

	for _, tc := range cases {
		err := validateSkillName(tc.name)
		if (err != nil) != tc.wantErr {
			t.Fatalf("validateSkillName(%q) err=%v wantErr=%v", tc.name, err, tc.wantErr)
		}
	}
}

func TestValidateSkillDescription(t *testing.T) {
	if err := validateSkillDescription("A short description."); err != nil {
		t.Fatalf("expected description to pass: %v", err)
	}
	if err := validateSkillDescription(""); err == nil {
		t.Fatalf("expected empty description to fail")
	}
	long := make([]rune, 1025)
	for i := range long {
		long[i] = 'a'
	}
	if err := validateSkillDescription(string(long)); err == nil {
		t.Fatalf("expected long description to fail")
	}
	if err := validateSkillDescription("bad <tag>"); err == nil {
		t.Fatalf("expected xml tag to fail")
	}
}
