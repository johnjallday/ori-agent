package assistantops

import (
	"slices"
	"strings"
	"testing"
)

func TestLoadSkill_IsValidVersionedAndNamesExactlyTheRegisteredOperations(t *testing.T) {
	skill, err := LoadSkill()
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != SkillName || skill.Version < 1 || strings.TrimSpace(skill.Description) == "" {
		t.Fatalf("skill metadata: %+v", skill)
	}
	declared, registered := slices.Clone(skill.Tools), ToolNames()
	slices.Sort(declared)
	slices.Sort(registered)
	if !slices.Equal(declared, registered) {
		t.Fatalf("the skill declares %v but the registry has %v", declared, registered)
	}
	prompt := skill.Prompt(ToolNames())
	for _, tool := range ToolNames() {
		if !strings.Contains(prompt, tool) {
			t.Errorf("the instructions never mention %s", tool)
		}
	}
	// What the instructions must teach: inspection, one resolved target, the
	// meaning of each result and the fallback, without claiming a view changed.
	for _, phrase := range []string{
		"Inspect", "Resolve one target", "only tool call", "`requested`", "`choose`", "`not_found`", "`unavailable`",
		"You are not told whether the", "Do not use it for", "a note, a task", "save nothing, start nothing and install nothing",
	} {
		if !strings.Contains(prompt, phrase) {
			t.Errorf("the instructions lost %q", phrase)
		}
	}
	if !strings.Contains(prompt, "grants nothing by itself") {
		t.Error("the loaded skill must say it grants nothing")
	}
}

func TestSkillPrompt_IsWithheldUnlessEveryNamedOperationIsOffered(t *testing.T) {
	skill, err := LoadSkill()
	if err != nil {
		t.Fatal(err)
	}
	for name, offered := range map[string][]string{
		"no tools":         nil,
		"readers only":     {"home_workspaces", "assistant_workspace_discovery"},
		"a lookalike name": {"assistant_operate_open_all"},
	} {
		if prompt := skill.Prompt(offered); prompt != "" {
			t.Errorf("%s: the skill must not be loaded, got %d characters", name, len(prompt))
		}
	}
	if skill.Prompt(append([]string{"home_workspaces"}, ToolNames()...)) == "" {
		t.Error("the skill loads beside other tools when its operations are offered")
	}
	if (Skill{}).Prompt(ToolNames()) != "" {
		t.Error("an unloaded skill has no instructions")
	}
}

func TestParseSkill_RefusesTextThatOutrunsTheRegistry(t *testing.T) {
	valid := "---\nname: operate-ori\ndescription: Operate the app.\nversion: 1\nallowed-tools:\n  - assistant_operate_open\n---\n\nUse `assistant_operate_open`.\n"
	if _, err := parseSkill(valid); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	cases := map[string]string{
		"undeclared operation in the text": strings.Replace(valid, "Use `assistant_operate_open`.", "Use `assistant_operate_open` then `assistant_operate_delete_workspace`.", 1),
		"unregistered operation declared":  strings.Replace(valid, "  - assistant_operate_open\n", "  - assistant_operate_open\n  - assistant_operate_run_shell\n", 1),
		"no declared operations":           strings.Replace(valid, "allowed-tools:\n  - assistant_operate_open\n", "", 1),
		"missing version":                  strings.Replace(valid, "version: 1\n", "", 1),
		"another skill's name":             strings.Replace(valid, "name: operate-ori", "name: something-else", 1),
		"empty instructions":               strings.Replace(valid, "\nUse `assistant_operate_open`.\n", "\n", 1),
	}
	for name, markdown := range cases {
		if _, err := parseSkill(markdown); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
}
