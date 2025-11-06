package health

import (
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name     string
		v1       string
		v2       string
		expected int
		wantErr  bool
	}{
		// Equal versions
		{"equal versions", "1.0.0", "1.0.0", 0, false},
		{"equal with v prefix", "v1.0.0", "v1.0.0", 0, false},
		{"equal mixed prefix", "v1.0.0", "1.0.0", 0, false},

		// v1 > v2
		{"major version greater", "2.0.0", "1.0.0", 1, false},
		{"minor version greater", "1.2.0", "1.1.0", 1, false},
		{"patch version greater", "1.0.2", "1.0.1", 1, false},

		// v1 < v2
		{"major version less", "1.0.0", "2.0.0", -1, false},
		{"minor version less", "1.1.0", "1.2.0", -1, false},
		{"patch version less", "1.0.1", "1.0.2", -1, false},

		// Dev versions
		{"dev version", "dev", "1.0.0", 0, false},
		{"version vs dev", "1.0.0", "dev", 0, false},

		// Pre-release versions
		{"pre-release ignored", "1.0.0-beta", "1.0.0", 0, false},
		{"pre-release comparison", "1.0.1-beta", "1.0.0", 1, false},

		// Invalid versions
		{"invalid v1", "1.0", "1.0.0", 0, true},
		{"invalid v2", "1.0.0", "1.0", 0, true},
		{"non-numeric", "1.a.0", "1.0.0", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CompareVersions(tt.v1, tt.v2)
			if (err != nil) != tt.wantErr {
				t.Errorf("CompareVersions(%q, %q) error = %v, wantErr %v", tt.v1, tt.v2, err, tt.wantErr)
				return
			}
			if !tt.wantErr && result != tt.expected {
				t.Errorf("CompareVersions(%q, %q) = %d, expected %d", tt.v1, tt.v2, result, tt.expected)
			}
		})
	}
}

func TestIsCompatible(t *testing.T) {
	tests := []struct {
		name          string
		agentVersion  string
		pluginVersion string
		minVersion    string
		maxVersion    string
		wantCompat    bool
	}{
		{
			name:          "no requirements",
			agentVersion:  "1.0.0",
			pluginVersion: "1.0.0",
			minVersion:    "",
			maxVersion:    "",
			wantCompat:    true,
		},
		{
			name:          "meets minimum",
			agentVersion:  "1.0.0",
			pluginVersion: "1.0.0",
			minVersion:    "0.9.0",
			maxVersion:    "",
			wantCompat:    true,
		},
		{
			name:          "below minimum",
			agentVersion:  "0.8.0",
			pluginVersion: "1.0.0",
			minVersion:    "0.9.0",
			maxVersion:    "",
			wantCompat:    false,
		},
		{
			name:          "within range",
			agentVersion:  "1.0.0",
			pluginVersion: "1.0.0",
			minVersion:    "0.9.0",
			maxVersion:    "2.0.0",
			wantCompat:    true,
		},
		{
			name:          "exceeds maximum",
			agentVersion:  "2.1.0",
			pluginVersion: "1.0.0",
			minVersion:    "0.9.0",
			maxVersion:    "2.0.0",
			wantCompat:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compatible, _ := IsCompatible(tt.agentVersion, tt.pluginVersion, tt.minVersion, tt.maxVersion)
			if compatible != tt.wantCompat {
				t.Errorf("IsCompatible() = %v, want %v", compatible, tt.wantCompat)
			}
		})
	}
}

func TestIsAPICompatible(t *testing.T) {
	tests := []struct {
		name              string
		agentAPIVersion   string
		pluginAPIVersion  string
		wantCompat        bool
	}{
		{"matching versions", "v1", "v1", true},
		{"mismatching versions", "v1", "v2", false},
		{"v0 vs v1", "v1", "v0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compatible, _ := IsAPICompatible(tt.agentAPIVersion, tt.pluginAPIVersion)
			if compatible != tt.wantCompat {
				t.Errorf("IsAPICompatible() = %v, want %v", compatible, tt.wantCompat)
			}
		})
	}
}
