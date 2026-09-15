package agent

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// retiredAgentTypeTokens are the spellings of the retired agent type. None may
// reappear in the host's code, templates, or static assets.
var retiredAgentTypeTokens = []string{
	"tool-calling",
	"TypeToolCalling",
	"TypeModels",
	"agent_type",
	"agentType",
	"suggested_agent_type",
}

// retiredAgentTypeAllowlist names files, relative to the repository root, that
// mention a retired token on purpose: this gate, and tests proving an API
// client that still sends or expects the retired key is handled.
var retiredAgentTypeAllowlist = map[string]bool{
	"internal/agent/retired_type_test.go":            true,
	"internal/agenthttp/agents_retired_type_test.go": true,
	"internal/agenthttp/auto_config_test.go":         true,
}

var retiredAgentTypeScannedExts = map[string]bool{
	".go": true, ".js": true, ".mjs": true, ".ts": true, ".tmpl": true, ".html": true,
	".css": true, ".json": true, ".md": true, ".yaml": true, ".yml": true, ".sql": true,
	".sh": true, ".txt": true,
}

func TestRetiredAgentTypeDoesNotReappear(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	var hits []string
	for _, top := range []string{"internal", "cmd"} {
		walkErr := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel == "internal/web/static/vendor" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !retiredAgentTypeScannedExts[filepath.Ext(path)] || retiredAgentTypeAllowlist[rel] {
				return nil
			}
			data, readErr := os.ReadFile(path) // #nosec G304 -- walking this repository's own source tree
			if readErr != nil {
				return readErr
			}
			if bytes.Contains(data, []byte("Code generated")) && bytes.Contains(data, []byte("DO NOT EDIT")) {
				return nil
			}
			scanner := bufio.NewScanner(bytes.NewReader(data))
			scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
			for line := 1; scanner.Scan(); line++ {
				text := scanner.Text()
				for _, token := range retiredAgentTypeTokens {
					if strings.Contains(text, token) {
						hits = append(hits, rel+":"+strconv.Itoa(line)+": "+token)
					}
				}
			}
			return scanner.Err()
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", top, walkErr)
		}
	}

	if len(hits) > 0 {
		t.Fatalf("the retired agent type reappeared (%d hits):\n%s", len(hits), strings.Join(hits, "\n"))
	}
}
