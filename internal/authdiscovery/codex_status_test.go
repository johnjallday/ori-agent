package authdiscovery

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadCodexLoginStatus(t *testing.T) {
	for _, tt := range []struct {
		name, file, keychain string
		want                 CodexLoginStatus
		wantErr              bool
	}{
		{name: "absent"},
		{name: "file subscription", file: `{"tokens":{"access_token":"invented-secret","account_id":"private"}}`, want: CodexLoginStatus{SignedIn: true}},
		{name: "keychain subscription", keychain: `{"tokens":{"access_token":"invented-secret"}}`, want: CodexLoginStatus{SignedIn: true}},
		{name: "API key", file: `{"auth_mode":"apikey","OPENAI_API_KEY":"invented-key"}`, want: CodexLoginStatus{SignedIn: true, APIKey: true}},
		{name: "keychain API key", keychain: `{"OPENAI_API_KEY":"invented-key"}`, want: CodexLoginStatus{SignedIn: true, APIKey: true}},
		{name: "empty tokens", file: `{"tokens":{}}`},
		{name: "malformed file", file: `{`, wantErr: true},
		{name: "malformed keychain", keychain: `{`, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if tt.file != "" {
				if err := os.WriteFile(filepath.Join(home, CodexCliAuthFilename), []byte(tt.file), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := readCodexLoginStatus(home, func() ([]byte, error) {
				if tt.file != "" {
					t.Fatal("file login must not query keychain")
				}
				if tt.keychain == "" {
					return nil, os.ErrNotExist
				}
				return []byte(tt.keychain), nil
			})
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got %+v, %v; want %+v error=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestCodexLoginStatusErrorsAreSanitized(t *testing.T) {
	_, err := readCodexLoginStatus(t.TempDir(), func() ([]byte, error) { return nil, errors.New("private diagnostic") })
	if err == nil || err.Error() != "Codex login status is unavailable" {
		t.Fatalf("got %v", err)
	}
}
