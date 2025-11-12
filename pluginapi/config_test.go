package pluginapi

import (
	"strings"
	"testing"
)

const validConfig = `
name: test-plugin
version: 1.0.0
description: A test plugin for unit testing
license: MIT
repository: https://github.com/test/test-plugin

maintainers:
  - name: Test User
    email: test@example.com

platforms:
  - os: darwin
    architectures: [amd64, arm64]
  - os: linux
    architectures: [amd64]

requirements:
  min_ori_version: "0.1.0"
  dependencies: []
`

func TestReadPluginConfig_Valid(t *testing.T) {
	config := ReadPluginConfig(validConfig)

	if config.Name != "test-plugin" {
		t.Errorf("Expected name 'test-plugin', got '%s'", config.Name)
	}
	if config.Version != "1.0.0" {
		t.Errorf("Expected version '1.0.0', got '%s'", config.Version)
	}
	if config.Description != "A test plugin for unit testing" {
		t.Errorf("Expected description 'A test plugin for unit testing', got '%s'", config.Description)
	}
	if config.License != "MIT" {
		t.Errorf("Expected license 'MIT', got '%s'", config.License)
	}
	if config.Repository != "https://github.com/test/test-plugin" {
		t.Errorf("Expected repository 'https://github.com/test/test-plugin', got '%s'", config.Repository)
	}
	if len(config.Platforms) != 2 {
		t.Errorf("Expected 2 platforms, got %d", len(config.Platforms))
	}
	if len(config.Maintainers) != 1 {
		t.Errorf("Expected 1 maintainer, got %d", len(config.Maintainers))
	}
	if config.Requirements.MinOriVersion != "0.1.0" {
		t.Errorf("Expected min_ori_version '0.1.0', got '%s'", config.Requirements.MinOriVersion)
	}
}

func TestReadPluginConfig_MissingName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for missing name field")
		} else if !strings.Contains(r.(string), "missing required field: name") {
			t.Errorf("Expected panic message to contain 'missing required field: name', got: %v", r)
		}
	}()

	yaml := strings.Replace(validConfig, "name: test-plugin", "", 1)
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_MissingVersion(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for missing version field")
		} else if !strings.Contains(r.(string), "missing required field: version") {
			t.Errorf("Expected panic message to contain 'missing required field: version', got: %v", r)
		}
	}()

	yaml := strings.Replace(validConfig, "version: 1.0.0", "", 1)
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_MissingDescription(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for missing description field")
		} else if !strings.Contains(r.(string), "missing required field: description") {
			t.Errorf("Expected panic message to contain 'missing required field: description', got: %v", r)
		}
	}()

	yaml := strings.Replace(validConfig, "description: A test plugin for unit testing", "", 1)
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_MissingLicense(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for missing license field")
		} else if !strings.Contains(r.(string), "missing required field: license") {
			t.Errorf("Expected panic message to contain 'missing required field: license', got: %v", r)
		}
	}()

	yaml := strings.Replace(validConfig, "license: MIT", "", 1)
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_MissingRepository(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for missing repository field")
		} else if !strings.Contains(r.(string), "missing required field: repository") {
			t.Errorf("Expected panic message to contain 'missing required field: repository', got: %v", r)
		}
	}()

	yaml := strings.Replace(validConfig, "repository: https://github.com/test/test-plugin", "", 1)
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_MissingPlatforms(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for missing platforms field")
		} else if !strings.Contains(r.(string), "missing required field: platforms") {
			t.Errorf("Expected panic message to contain 'missing required field: platforms', got: %v", r)
		}
	}()

	yaml := `
name: test-plugin
version: 1.0.0
description: A test plugin
license: MIT
repository: https://github.com/test/test-plugin
maintainers:
  - name: Test User
    email: test@example.com
platforms: []
`
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_MissingMaintainers(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for missing maintainers field")
		} else if !strings.Contains(r.(string), "missing required field: maintainers") {
			t.Errorf("Expected panic message to contain 'missing required field: maintainers', got: %v", r)
		}
	}()

	yaml := `
name: test-plugin
version: 1.0.0
description: A test plugin
license: MIT
repository: https://github.com/test/test-plugin
maintainers: []
platforms:
  - os: darwin
    architectures: [amd64]
`
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_InvalidSemverVersion(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for invalid semver version")
		} else if !strings.Contains(r.(string), "invalid semver format for version") {
			t.Errorf("Expected panic message to contain 'invalid semver format for version', got: %v", r)
		}
	}()

	yaml := strings.Replace(validConfig, "version: 1.0.0", "version: invalid", 1)
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_InvalidSemverMinOriVersion(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for invalid semver min_ori_version")
		} else if !strings.Contains(r.(string), "invalid semver format for min_ori_version") {
			t.Errorf("Expected panic message to contain 'invalid semver format for min_ori_version', got: %v", r)
		}
	}()

	yaml := strings.Replace(validConfig, `min_ori_version: "0.1.0"`, `min_ori_version: "invalid"`, 1)
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_InvalidURL(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for invalid repository URL")
		} else if !strings.Contains(r.(string), "invalid URL format for repository") {
			t.Errorf("Expected panic message to contain 'invalid URL format for repository', got: %v", r)
		}
	}()

	yaml := strings.Replace(validConfig, "repository: https://github.com/test/test-plugin", "repository: not-a-url", 1)
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_PlatformMissingOS(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for platform missing os field")
		} else if !strings.Contains(r.(string), "platform[0] missing os field") {
			t.Errorf("Expected panic message to contain 'platform[0] missing os field', got: %v", r)
		}
	}()

	yaml := `
name: test-plugin
version: 1.0.0
description: A test plugin
license: MIT
repository: https://github.com/test/test-plugin
maintainers:
  - name: Test User
    email: test@example.com
platforms:
  - architectures: [amd64]
`
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_PlatformEmptyArchitectures(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for platform with empty architectures")
		} else if !strings.Contains(r.(string), "platform[0] has empty architectures array") {
			t.Errorf("Expected panic message to contain 'platform[0] has empty architectures array', got: %v", r)
		}
	}()

	yaml := `
name: test-plugin
version: 1.0.0
description: A test plugin
license: MIT
repository: https://github.com/test/test-plugin
maintainers:
  - name: Test User
    email: test@example.com
platforms:
  - os: darwin
    architectures: []
`
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_MaintainerMissingName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for maintainer missing name")
		} else if !strings.Contains(r.(string), "maintainer[0] missing name field") {
			t.Errorf("Expected panic message to contain 'maintainer[0] missing name field', got: %v", r)
		}
	}()

	yaml := `
name: test-plugin
version: 1.0.0
description: A test plugin
license: MIT
repository: https://github.com/test/test-plugin
maintainers:
  - email: test@example.com
platforms:
  - os: darwin
    architectures: [amd64]
`
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_MaintainerMissingEmail(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for maintainer missing email")
		} else if !strings.Contains(r.(string), "maintainer[0] missing email field") {
			t.Errorf("Expected panic message to contain 'maintainer[0] missing email field', got: %v", r)
		}
	}()

	yaml := `
name: test-plugin
version: 1.0.0
description: A test plugin
license: MIT
repository: https://github.com/test/test-plugin
maintainers:
  - name: Test User
platforms:
  - os: darwin
    architectures: [amd64]
`
	ReadPluginConfig(yaml)
}

func TestReadPluginConfig_MalformedYAML(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for malformed YAML")
		} else if !strings.Contains(r.(string), "invalid plugin config YAML") {
			t.Errorf("Expected panic message to contain 'invalid plugin config YAML', got: %v", r)
		}
	}()

	yaml := `this is: not: valid: yaml:`
	ReadPluginConfig(yaml)
}

func TestToMetadata(t *testing.T) {
	config := ReadPluginConfig(validConfig)
	metadata, err := config.ToMetadata()
	if err != nil {
		t.Fatalf("ToMetadata() returned error: %v", err)
	}

	// Test all fields
	if metadata.Name != "test-plugin" {
		t.Errorf("Expected name 'test-plugin', got '%s'", metadata.Name)
	}
	if metadata.Version != "1.0.0" {
		t.Errorf("Expected version '1.0.0', got '%s'", metadata.Version)
	}
	if metadata.Description != "A test plugin for unit testing" {
		t.Errorf("Expected description 'A test plugin for unit testing', got '%s'", metadata.Description)
	}
	if metadata.License != "MIT" {
		t.Errorf("Expected license 'MIT', got '%s'", metadata.License)
	}
	if metadata.Repository != "https://github.com/test/test-plugin" {
		t.Errorf("Expected repository 'https://github.com/test/test-plugin', got '%s'", metadata.Repository)
	}
	if len(metadata.Maintainers) != 1 {
		t.Errorf("Expected 1 maintainer, got %d", len(metadata.Maintainers))
	}
	if metadata.Maintainers[0].Name != "Test User" {
		t.Errorf("Expected maintainer name 'Test User', got '%s'", metadata.Maintainers[0].Name)
	}
	if metadata.Maintainers[0].Email != "test@example.com" {
		t.Errorf("Expected maintainer email 'test@example.com', got '%s'", metadata.Maintainers[0].Email)
	}
	if len(metadata.Platforms) != 2 {
		t.Errorf("Expected 2 platforms, got %d", len(metadata.Platforms))
	}
	if metadata.Platforms[0].Os != "darwin" {
		t.Errorf("Expected platform OS 'darwin', got '%s'", metadata.Platforms[0].Os)
	}
	if len(metadata.Platforms[0].Architectures) != 2 {
		t.Errorf("Expected 2 architectures, got %d", len(metadata.Platforms[0].Architectures))
	}
	if metadata.Requirements.MinOriVersion != "0.1.0" {
		t.Errorf("Expected min_ori_version '0.1.0', got '%s'", metadata.Requirements.MinOriVersion)
	}
}
