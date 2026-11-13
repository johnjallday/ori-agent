package knownfolder

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRedirectedSystemAnswers(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"OneDrive/Documents", "Dokumente", "Bureau"} {
		path := filepath.Join(home, name)
		if err := os.MkdirAll(path, 0750); err != nil {
			t.Fatal(err)
		}
		got, err := Resolve(home, "documents", func(_, _ string) (string, error) { return path, nil })
		want, _ := filepath.EvalSymlinks(path)
		if err != nil || got != want {
			t.Fatal("lost system redirection", got, err)
		}
	}
}

func TestResolveUsesOnlyTheKnownLocation(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"desktop", "documents", "downloads"} {
		path, _ := englishPath(home, id)
		if _, err := Resolve(home, id, englishPath); !errors.Is(err, ErrUnavailable) {
			t.Fatal("missing folder resolved", id, err)
		}
		if err := os.Mkdir(path, 0750); err != nil {
			t.Fatal(err)
		}
		got, err := Resolve(home, id, englishPath)
		want, _ := filepath.EvalSymlinks(path)
		if err != nil || got != want {
			t.Fatal(id, got, err)
		}
	}
	for _, answer := range []string{home, filepath.Join(home, "absent"), "relative", ""} {
		if _, err := Resolve(home, "desktop", func(_, _ string) (string, error) { return answer, nil }); !errors.Is(err, ErrUnavailable) {
			t.Fatal("unsafe or absent lookup resolved", answer, err)
		}
	}
	if _, err := Resolve(home, "music", englishPath); !errors.Is(err, ErrUnavailable) {
		t.Fatal("Music became a catch-all")
	}
}
