package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/database"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
)

func TestResolveDataDirectory(t *testing.T) {
	launchDir := filepath.Join(string(filepath.Separator), "tmp", "ori-launch")
	homeDir := filepath.Join(string(filepath.Separator), "Users", "test")

	tests := []struct {
		name           string
		configuredDir  string
		executablePath string
		goos           string
		homeDir        string
		workingDir     string
		hasDataFiles   bool
		want           string
	}{
		{
			name:          "absolute explicit override wins",
			configuredDir: filepath.Join(string(filepath.Separator), "tmp", "explicit-ori"),
			workingDir:    launchDir,
			goos:          "darwin",
			homeDir:       homeDir,
			want:          filepath.Join(string(filepath.Separator), "tmp", "explicit-ori"),
		},
		{
			name:          "relative explicit override is anchored to launch directory",
			configuredDir: "isolated-data",
			workingDir:    launchDir,
			goos:          "darwin",
			homeDir:       homeDir,
			want:          filepath.Join(launchDir, "isolated-data"),
		},
		{
			name:           "installed mac app uses application support",
			executablePath: filepath.Join(string(filepath.Separator), "Applications", "Ori Agent.app", "Contents", "MacOS", "ori-agent"),
			workingDir:     launchDir,
			goos:           "darwin",
			homeDir:        homeDir,
			want:           filepath.Join(homeDir, "Library", "Application Support", "OriAgent"),
		},
		{
			name:       "existing ori-data directory stays authoritative",
			workingDir: filepath.Join(launchDir, "ori-data"),
			goos:       "darwin",
			homeDir:    homeDir,
			want:       filepath.Join(launchDir, "ori-data"),
		},
		{
			name:         "directory containing state stays authoritative",
			workingDir:   filepath.Join(launchDir, "existing-profile"),
			hasDataFiles: true,
			goos:         "darwin",
			homeDir:      homeDir,
			want:         filepath.Join(launchDir, "existing-profile"),
		},
		{
			name:       "clean standalone launch gets local ori-data",
			workingDir: launchDir,
			goos:       "darwin",
			homeDir:    homeDir,
			want:       filepath.Join(launchDir, "ori-data"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveDataDirectory(dataDirectoryInputs{
				configuredDir:  tt.configuredDir,
				executablePath: tt.executablePath,
				goos:           tt.goos,
				homeDir:        tt.homeDir,
				workingDir:     tt.workingDir,
				hasDataFiles:   tt.hasDataFiles,
			})
			if err != nil {
				t.Fatalf("resolveDataDirectory: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveDataDirectory() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveDataDirectory_InstalledAppRequiresHome(t *testing.T) {
	_, err := resolveDataDirectory(dataDirectoryInputs{
		executablePath: "/Applications/Ori Agent.app/Contents/MacOS/ori-agent",
		goos:           "darwin",
		workingDir:     t.TempDir(),
	})
	if err == nil {
		t.Fatal("installed app resolution must fail when the home directory is unavailable")
	}
}

func TestActivateDataDirectory_PublishesOneCanonicalRoot(t *testing.T) {
	launchDir := t.TempDir()
	dataDir := filepath.Join(launchDir, "runtime-data")
	originalCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	originalDataDir, hadDataDir := os.LookupEnv("ORI_DATA_DIR")
	t.Cleanup(func() {
		if err := os.Chdir(originalCWD); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
		if hadDataDir {
			if err := os.Setenv("ORI_DATA_DIR", originalDataDir); err != nil {
				t.Errorf("restore ORI_DATA_DIR: %v", err)
			}
		} else if err := os.Unsetenv("ORI_DATA_DIR"); err != nil {
			t.Errorf("unset ORI_DATA_DIR: %v", err)
		}
	})

	if err := activateDataDirectory(dataDir); err != nil {
		t.Fatalf("activateDataDirectory: %v", err)
	}

	want, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got := os.Getenv("ORI_DATA_DIR"); got != want {
		t.Fatalf("ORI_DATA_DIR = %q, want %q", got, want)
	}
	if got, err := os.Getwd(); err != nil || got != want {
		t.Fatalf("cwd = %q, %v; want %q", got, err, want)
	}
	if got := config.DefaultDataDir(); got != want {
		t.Fatalf("config.DefaultDataDir() = %q, want %q", got, want)
	}
	if got := config.UnconfirmedWorkspaceRoot(); got != filepath.Join(want, "workspace-staging") {
		t.Fatalf("config.UnconfirmedWorkspaceRoot() = %q", got)
	}
	if _, err := os.Stat(filepath.Join(want, "vaults")); err != nil {
		t.Fatalf("vaults directory: %v", err)
	}
	configManager := config.NewManager("settings.json")
	if err := configManager.Load(); err != nil {
		t.Fatalf("config Load: %v", err)
	}
	if err := configManager.Save(); err != nil {
		t.Fatalf("config Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(want, "settings.json")); err != nil {
		t.Fatalf("settings path: %v", err)
	}
	onboardingManager := onboarding.NewManager("app_state.json")
	if err := onboardingManager.SetCurrentStep(1); err != nil {
		t.Fatalf("persist onboarding state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(want, "app_state.json")); err != nil {
		t.Fatalf("onboarding path: %v", err)
	}
	resetHandler := settingshttp.NewResetHandler(onboardingManager, nil, config.DefaultDataDir())
	if got := resetHandler.DataDir(); got != want {
		t.Fatalf("reset data directory = %q, want %q", got, want)
	}

	db, err := database.Open(context.Background(), nil)
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	if got := db.Path(); got != filepath.Join(want, "sessions.db") {
		t.Fatalf("database path = %q, want %q", got, filepath.Join(want, "sessions.db"))
	}
}

func TestActivateDataDirectory_DoesNotAdoptHomeStaging(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Application Support regression")
	}

	homeDir := t.TempDir()
	foreignStaging := filepath.Join(homeDir, "Library", "Application Support", "OriAgent", "workspace-staging")
	if err := os.MkdirAll(foreignStaging, 0o755); err != nil {
		t.Fatalf("seed foreign staging: %v", err)
	}
	marker := filepath.Join(foreignStaging, "foreign-workspace.json")
	if err := os.WriteFile(marker, []byte(`{"id":"foreign"}`), 0o600); err != nil {
		t.Fatalf("seed foreign marker: %v", err)
	}

	launchDir := t.TempDir()
	dataDir, err := resolveDataDirectory(dataDirectoryInputs{
		workingDir: launchDir,
		goos:       runtime.GOOS,
		homeDir:    homeDir,
	})
	if err != nil {
		t.Fatalf("resolveDataDirectory: %v", err)
	}
	if err := activateDataDirectoryForTest(t, dataDir); err != nil {
		t.Fatalf("activateDataDirectory: %v", err)
	}

	canonicalDataDir, err := filepath.EvalSymlinks(filepath.Join(launchDir, "ori-data"))
	if err != nil {
		t.Fatalf("canonical data directory: %v", err)
	}
	wantStaging := filepath.Join(canonicalDataDir, "workspace-staging")
	if got := config.UnconfirmedWorkspaceRoot(); got != wantStaging {
		t.Fatalf("staging root = %q, want %q", got, wantStaging)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("foreign staging marker was modified: %v", err)
	}
}

func activateDataDirectoryForTest(t *testing.T, dataDir string) error {
	t.Helper()
	originalCWD, err := os.Getwd()
	if err != nil {
		return err
	}
	originalDataDir, hadDataDir := os.LookupEnv("ORI_DATA_DIR")
	t.Cleanup(func() {
		if err := os.Chdir(originalCWD); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
		if hadDataDir {
			if err := os.Setenv("ORI_DATA_DIR", originalDataDir); err != nil {
				t.Errorf("restore ORI_DATA_DIR: %v", err)
			}
		} else if err := os.Unsetenv("ORI_DATA_DIR"); err != nil {
			t.Errorf("unset ORI_DATA_DIR: %v", err)
		}
	})
	return activateDataDirectory(dataDir)
}
