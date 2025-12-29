package device

import "testing"

func TestDetectVendorFromName(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		// Apple
		{"Apple M1", "Apple"},
		{"Apple M2 Pro", "Apple"},
		{"Apple M3 Max", "Apple"},

		// NVIDIA
		{"NVIDIA GeForce RTX 4090", "NVIDIA"},
		{"GeForce GTX 1080", "NVIDIA"},
		{"Quadro RTX 8000", "NVIDIA"},
		{"RTX 3090 Ti", "NVIDIA"},

		// AMD
		{"AMD Radeon RX 7900 XTX", "AMD"},
		{"Radeon Pro 5500M", "AMD"},
		{"ATI Radeon HD 5870", "AMD"},

		// Intel
		{"Intel UHD Graphics 630", "Intel"},
		{"Intel Iris Xe Graphics", "Intel"},
		{"Intel Arc A770", "Intel"},

		// Unknown
		{"Some Unknown GPU", "Unknown"},
		{"", "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectVendorFromName(tt.name)
			if result != tt.expected {
				t.Errorf("DetectVendorFromName(%q) = %q, want %q", tt.name, result, tt.expected)
			}
		})
	}
}

func TestParseVRAM(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		// GB values
		{"8 GB", 8 * GB},
		{"16 GB", 16 * GB},
		{"24 GB", 24 * GB},
		{"4.5 GB", int64(4.5 * float64(GB))},

		// MB values
		{"4096 MB", 4096 * MB},
		{"8192 MB", 8192 * MB},
		{"512 MB", 512 * MB},

		// KB values
		{"1024 KB", 1024 * KB},

		// Edge cases
		{"", 0},
		{"invalid", 0},
		{"8", 0},    // Missing unit
		{"GB 8", 0}, // Wrong order
		{"   ", 0},  // Whitespace only
		{"8 TB", 8}, // Unknown unit (falls through to default)
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ParseVRAM(tt.input)
			if result != tt.expected {
				t.Errorf("ParseVRAM(%q) = %d, want %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNewAppleSiliconGPU(t *testing.T) {
	gpu := NewAppleSiliconGPU()

	if gpu == nil {
		t.Fatal("NewAppleSiliconGPU returned nil")
	}

	if gpu.Vendor != "Apple" {
		t.Errorf("Vendor = %q, want %q", gpu.Vendor, "Apple")
	}

	if !gpu.IsAppleSilicon {
		t.Error("IsAppleSilicon should be true")
	}

	if gpu.IsDiscrete {
		t.Error("IsDiscrete should be false")
	}
}

func TestByteConstants(t *testing.T) {
	// Verify byte constants are correct
	if KB != 1024 {
		t.Errorf("KB = %d, want 1024", KB)
	}
	if MB != 1024*1024 {
		t.Errorf("MB = %d, want %d", MB, 1024*1024)
	}
	if GB != 1024*1024*1024 {
		t.Errorf("GB = %d, want %d", GB, 1024*1024*1024)
	}
}
