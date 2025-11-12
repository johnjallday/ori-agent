package types

import "testing"

func TestPluginRegistryEntry_IsCompatibleWith(t *testing.T) {
	tests := []struct {
		name     string
		entry    PluginRegistryEntry
		platform string
		want     bool
	}{
		{
			name: "exact platform match",
			entry: PluginRegistryEntry{
				Platforms: []string{"darwin-arm64", "linux-amd64"},
			},
			platform: "darwin-arm64",
			want:     true,
		},
		{
			name: "platform not in list",
			entry: PluginRegistryEntry{
				Platforms: []string{"darwin-arm64", "linux-amd64"},
			},
			platform: "windows-amd64",
			want:     false,
		},
		{
			name: "all platforms supported",
			entry: PluginRegistryEntry{
				Platforms: []string{"all"},
			},
			platform: "freebsd-386",
			want:     true,
		},
		{
			name: "empty platforms - fallback to SupportedOS/Arch",
			entry: PluginRegistryEntry{
				Platforms:     []string{},
				SupportedOS:   []string{"darwin", "linux"},
				SupportedArch: []string{"amd64", "arm64"},
			},
			platform: "darwin-arm64",
			want:     true,
		},
		{
			name: "empty platforms - fallback fails",
			entry: PluginRegistryEntry{
				Platforms:     []string{},
				SupportedOS:   []string{"darwin"},
				SupportedArch: []string{"amd64"},
			},
			platform: "linux-arm64",
			want:     false,
		},
		{
			name: "unknown platform - fallback to SupportedOS/Arch",
			entry: PluginRegistryEntry{
				Platforms:     []string{"unknown"},
				SupportedOS:   []string{"linux"},
				SupportedArch: []string{"amd64"},
			},
			platform: "linux-amd64",
			want:     true,
		},
		{
			name: "invalid platform format",
			entry: PluginRegistryEntry{
				Platforms: []string{},
			},
			platform: "invalid",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.IsCompatibleWith(tt.platform); got != tt.want {
				t.Errorf("IsCompatibleWith() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPluginRegistryEntry_IsCompatibleWithSystem(t *testing.T) {
	tests := []struct {
		name  string
		entry PluginRegistryEntry
		os    string
		arch  string
		want  bool
	}{
		{
			name: "os and arch match",
			entry: PluginRegistryEntry{
				SupportedOS:   []string{"darwin", "linux"},
				SupportedArch: []string{"amd64", "arm64"},
			},
			os:   "darwin",
			arch: "arm64",
			want: true,
		},
		{
			name: "os matches but arch doesn't",
			entry: PluginRegistryEntry{
				SupportedOS:   []string{"darwin"},
				SupportedArch: []string{"amd64"},
			},
			os:   "darwin",
			arch: "arm64",
			want: false,
		},
		{
			name: "all os supported",
			entry: PluginRegistryEntry{
				SupportedOS:   []string{"all"},
				SupportedArch: []string{"amd64"},
			},
			os:   "freebsd",
			arch: "amd64",
			want: true,
		},
		{
			name: "empty supported os - assume all",
			entry: PluginRegistryEntry{
				SupportedOS:   []string{},
				SupportedArch: []string{},
			},
			os:   "windows",
			arch: "amd64",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.IsCompatibleWithSystem(tt.os, tt.arch); got != tt.want {
				t.Errorf("IsCompatibleWithSystem() = %v, want %v", got, tt.want)
			}
		})
	}
}
