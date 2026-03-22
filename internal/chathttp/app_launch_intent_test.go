package chathttp

import "testing"

func TestInferOpenAppCommandFromChat(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCmd   string
		wantMatch bool
	}{
		{
			name:      "basic open command",
			input:     "open safari",
			wantCmd:   "/openapp safari",
			wantMatch: true,
		},
		{
			name:      "launch with suffix",
			input:     "launch Obsidian app",
			wantCmd:   "/openapp obsidian",
			wantMatch: true,
		},
		{
			name:      "polite prefix and filler",
			input:     "please open up visual studio code for me",
			wantCmd:   "/openapp visual studio code",
			wantMatch: true,
		},
		{
			name:      "already slash command",
			input:     "/openapp Safari",
			wantMatch: false,
		},
		{
			name:      "url should not match",
			input:     "open https://example.com",
			wantMatch: false,
		},
		{
			name:      "folder path should not match",
			input:     "open /Applications",
			wantMatch: false,
		},
		{
			name:      "email intent should not match",
			input:     "open my email inbox",
			wantMatch: false,
		},
		{
			name:      "multi-step prompt should not match",
			input:     "open safari and go to github",
			wantMatch: false,
		},
		{
			name:      "quoted app name",
			input:     "open \"obsidian\"",
			wantCmd:   "/openapp obsidian",
			wantMatch: true,
		},
		{
			name:      "workspace note follow-up should not match",
			input:     "start another note",
			wantMatch: false,
		},
		{
			name:      "workspace separate note should not match",
			input:     "start a separate note",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCmd, gotMatch := inferOpenAppCommandFromChat(tt.input)
			if gotMatch != tt.wantMatch {
				t.Fatalf("match = %v, want %v (cmd=%q)", gotMatch, tt.wantMatch, gotCmd)
			}
			if gotCmd != tt.wantCmd {
				t.Fatalf("cmd = %q, want %q", gotCmd, tt.wantCmd)
			}
		})
	}
}
