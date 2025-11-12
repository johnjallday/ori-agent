package registry

import (
	"reflect"
	"testing"
)

func Test_extractPlatformsFromAssets(t *testing.T) {
	tests := []struct {
		name   string
		assets []string
		want   []string
	}{
		{
			name: "standard format with hyphen",
			assets: []string{
				"plugin-v1.0.0-darwin-arm64",
				"plugin-v1.0.0-darwin-amd64",
				"plugin-v1.0.0-linux-amd64",
			},
			want: []string{"darwin-amd64", "darwin-arm64", "linux-amd64"},
		},
		{
			name: "format with underscore",
			assets: []string{
				"plugin_v1.0.0_linux_amd64.tar.gz",
				"plugin_v1.0.0_windows_amd64.exe",
			},
			want: []string{"linux-amd64", "windows-amd64"},
		},
		{
			name: "mixed formats",
			assets: []string{
				"plugin-darwin-arm64",
				"plugin_linux_amd64.tar.gz",
				"plugin-windows-amd64.exe",
			},
			want: []string{"darwin-arm64", "linux-amd64", "windows-amd64"},
		},
		{
			name: "with 32-bit architectures",
			assets: []string{
				"plugin-linux-386",
				"plugin-linux-arm",
				"plugin-freebsd-amd64",
			},
			want: []string{"freebsd-amd64", "linux-386", "linux-arm"},
		},
		{
			name:   "no platform information",
			assets: []string{"plugin.zip", "README.md", "checksums.txt"},
			want:   []string{},
		},
		{
			name: "duplicates are deduplicated",
			assets: []string{
				"plugin-darwin-arm64.zip",
				"plugin-darwin-arm64.tar.gz",
				"plugin-darwin-arm64.sha256",
			},
			want: []string{"darwin-arm64"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPlatformsFromAssets(tt.assets)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("extractPlatformsFromAssets() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_extractOSAndArch(t *testing.T) {
	tests := []struct {
		name      string
		platforms []string
		wantOS    []string
		wantArch  []string
	}{
		{
			name:      "multiple platforms",
			platforms: []string{"darwin-arm64", "darwin-amd64", "linux-amd64", "windows-amd64"},
			wantOS:    []string{"darwin", "linux", "windows"},
			wantArch:  []string{"amd64", "arm64"},
		},
		{
			name:      "single OS multiple arch",
			platforms: []string{"linux-amd64", "linux-arm64", "linux-386"},
			wantOS:    []string{"linux"},
			wantArch:  []string{"386", "amd64", "arm64"},
		},
		{
			name:      "single platform",
			platforms: []string{"darwin-arm64"},
			wantOS:    []string{"darwin"},
			wantArch:  []string{"arm64"},
		},
		{
			name:      "empty input",
			platforms: []string{},
			wantOS:    []string{},
			wantArch:  []string{},
		},
		{
			name:      "with freebsd",
			platforms: []string{"freebsd-amd64", "freebsd-386"},
			wantOS:    []string{"freebsd"},
			wantArch:  []string{"386", "amd64"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOS, gotArch := extractOSAndArch(tt.platforms)
			if !reflect.DeepEqual(gotOS, tt.wantOS) {
				t.Errorf("extractOSAndArch() gotOS = %v, want %v", gotOS, tt.wantOS)
			}
			if !reflect.DeepEqual(gotArch, tt.wantArch) {
				t.Errorf("extractOSAndArch() gotArch = %v, want %v", gotArch, tt.wantArch)
			}
		})
	}
}
