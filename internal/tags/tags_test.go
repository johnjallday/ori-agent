package tags

import "testing"

func TestNormalizeTag(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"dev_tools", "dev-tools"},
		{"DevTools", "dev-tools"},
		{"devTools", "dev-tools"},
		{"  audio  ", "audio"},
		{"file management", "file-management"},
		{"audio---tools", "audio-tools"},
		{"_audio_tools_", "audio-tools"},
		{"", ""},
		{"   ", ""},
	}

	for _, tt := range tests {
		if got := NormalizeTag(tt.in); got != tt.want {
			t.Fatalf("NormalizeTag(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestValidateTag(t *testing.T) {
	ok := []string{
		"audio",
		"dev-tools",
		"v2",
		"a1",
		"social-media",
	}

	for _, tag := range ok {
		if err := ValidateTag(tag); err != nil {
			t.Fatalf("ValidateTag(%q) unexpected error: %v", tag, err)
		}
	}

	bad := []string{
		"",
		"a",
		"-audio",
		"audio-",
		"audio--tools",
		"dev_tools",
		"DevTools",
		"audio.tools",
	}

	for _, tag := range bad {
		if err := ValidateTag(tag); err == nil {
			t.Fatalf("ValidateTag(%q) expected error", tag)
		}
	}
}

func TestValidateTags(t *testing.T) {
	valid, errs := ValidateTags([]string{"dev_tools", "a", "audio"})
	if len(valid) != 2 {
		t.Fatalf("ValidateTags valid len = %d, want 2", len(valid))
	}
	if valid[0] != "dev-tools" || valid[1] != "audio" {
		t.Fatalf("ValidateTags valid = %#v, want [\"dev-tools\" \"audio\"]", valid)
	}
	if len(errs) != 1 {
		t.Fatalf("ValidateTags errs len = %d, want 1", len(errs))
	}
}

func TestNormalizeTags(t *testing.T) {
	out := NormalizeTags([]string{"dev_tools", "dev-tools", "audio", "a", "audio", "social-media", "api", "utility"})
	want := []string{"dev-tools", "audio", "social-media", "api", "utility"}
	if len(out) != len(want) {
		t.Fatalf("NormalizeTags len = %d, want %d (%v)", len(out), len(want), out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("NormalizeTags[%d] = %q, want %q (%v)", i, out[i], want[i], out)
		}
	}

	limited := NormalizeTags([]string{"a1", "b2", "c3", "d4", "e5", "f6"})
	if len(limited) != 5 {
		t.Fatalf("NormalizeTags max len = %d, want 5 (%v)", len(limited), limited)
	}
}
