package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func inventoryFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	return NewManager(ManagerConfig{AgentStorePath: filepath.Join(root, "agents.json"), PersonalSkillsDir: filepath.Join(root, "Skills")}), root
}

func inventorySkill(t *testing.T, root, name, content, metadata string) {
	t.Helper()
	dir := filepath.Join(root, "Skills", name)
	if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if metadata != "" {
		if err := os.WriteFile(filepath.Join(dir, "agents", "openai.yaml"), []byte(metadata), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSkillInventoryReusesRecognizedMetadataWithoutPromptRawYAMLOrGrants(t *testing.T) {
	manager, root := inventoryFixture(t)
	inventorySkill(t, root, "declared", "---\nname: declared\ndescription: Telegram community assistance\nrequired_mcp_servers: [telegram]\nallowed_tools: [send-message]\n---\nPRIVATE_PROMPT_SENTINEL", "interface:\n  default_prompt: PRIVATE_YAML_SENTINEL\n")
	inventorySkill(t, root, "unsupported", "---\nname: unsupported\ndescription: nested metadata\nmcp:\n  server: invented-connection\n---\nPRIVATE_PROMPT_SENTINEL", "")
	inventorySkill(t, root, "openai", "---\nname: openai\ndescription: metadata dependency\n---\nPRIVATE_PROMPT_SENTINEL", "dependencies:\n  mcp_servers: [configured-name]\n  tools: [named-tool]\nprivate: PRIVATE_YAML_SENTINEL\n")
	result := manager.PersonalSkillInventory(context.Background(), 8)
	if result.State != "available" || len(result.Skills) != 3 {
		t.Fatalf("inventory: %+v", result)
	}
	byName := map[string]SkillMetadata{}
	for _, meta := range result.Skills {
		byName[meta.Name] = meta
	}
	if !reflect.DeepEqual(byName["declared"].RequiredMCPServers, []string{"telegram"}) || len(byName["unsupported"].RequiredMCPServers) != 0 || !reflect.DeepEqual(byName["openai"].RequiredMCPServers, []string{"configured-name"}) {
		t.Fatalf("dependency parser diverged: %+v", byName)
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"PRIVATE_PROMPT_SENTINEL", "PRIVATE_YAML_SENTINEL", "invented-connection", root, "trusted", "enabled"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("inventory leaked %s: %s", forbidden, encoded)
		}
	}
	// The UI and inventory use the same parsers/projection, not two frontmatter
	// interpretations. Reading inventory writes neither source nor skill state.
	entries, err := manager.PersonalSkills()
	if err != nil || !reflect.DeepEqual(MetadataInventory(entries, 8), result) {
		t.Fatalf("UI projection differs: %v %+v", err, entries)
	}
	if _, err := os.Stat(filepath.Join(root, "agents")); !os.IsNotExist(err) {
		t.Fatal("inventory wrote agent state")
	}
}

func TestSkillInventoryLargePromptDoesNotRequireAnUnboundedRead(t *testing.T) {
	manager, root := inventoryFixture(t)
	body := "---\nname: large\ndescription: bounded metadata\n---\n" + strings.Repeat("PRIVATE_PROMPT_SENTINEL", 100000)
	inventorySkill(t, root, "large", body, "")
	result := manager.PersonalSkillInventory(context.Background(), 8)
	if result.State != "available" || len(result.Skills) != 1 || result.Skills[0].Name != "large" {
		t.Fatalf("large prompt hid usable metadata: %+v", result)
	}
	// Existing explicit prompt reads still work: only summary reads are bounded.
	full, err := manager.loadSkillEntry(filepath.Join(root, "Skills", "large", "SKILL.md"), "large", SourcePersonal, filepath.Join(root, "Skills", "large"), true)
	if err != nil || len(full.Prompt) < 1<<20 {
		t.Fatalf("full prompt owner changed: %v", err)
	}
}

func TestSkillInventoryReportsPartialForOversizeMalformedAndClippedMetadata(t *testing.T) {
	manager, root := inventoryFixture(t)
	inventorySkill(t, root, "too-big", "---\nname: too-big\ndescription: "+strings.Repeat("x", metadataFileBytes)+"\n---\n", "")
	inventorySkill(t, root, "bad-yaml", "---\nname: [bad\n---\n", "")
	inventorySkill(t, root, "wide", "---\nname: wide\ndescription: "+strings.Repeat("界", 1200)+"\n---\n", strings.Repeat("x", metadataFileBytes+1))
	result := manager.PersonalSkillInventory(context.Background(), 8)
	if result.State != "partial" || result.Omitted != 1 || !result.Truncated || len(result.Skills) != 2 {
		t.Fatalf("partial: %+v", result)
	}
	for _, meta := range result.Skills {
		if len([]rune(meta.Description)) > 800 {
			t.Fatalf("unbounded description: %d", len([]rune(meta.Description)))
		}
	}
}

func TestSkillInventoryBoundsDirectoryWorkAndCancellation(t *testing.T) {
	manager, root := inventoryFixture(t)
	for i := 0; i < inventoryEntryLimit+5; i++ {
		inventorySkill(t, root, fmt.Sprintf("skill-%03d", i), fmt.Sprintf("---\nname: skill-%03d\ndescription: test\n---\n", i), "")
	}
	result := manager.PersonalSkillInventory(context.Background(), 8)
	if result.State != "partial" || len(result.Skills) != 8 || !result.Truncated {
		t.Fatalf("cap: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := manager.PersonalSkillInventory(ctx, 8); got.State != "unavailable" || len(got.Skills) != 0 {
		t.Fatalf("cancel: %+v", got)
	}
	var absent *Manager
	if got := absent.PersonalSkillInventory(context.Background(), 8); got.State != "unavailable" {
		t.Fatalf("absent: %+v", got)
	}
}
