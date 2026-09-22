package workspace

import (
	"encoding/json"
	"strings"
)

// BlueprintInputsSharedDataKey is where a workspace records the values it was
// created with, when its blueprint declared any. It sits next to
// workspace_bootstrap because it answers the same kind of question for the
// workspace's agents: what the user said this workspace is, in the blueprint's
// own vocabulary.
//
// The key lives here rather than beside the declaration itself so both the
// package that writes it and the prompt builders that read it can name it
// without one importing the other. The writer owns the shape; this file reads
// only the parts a prompt needs.
const BlueprintInputsSharedDataKey = "blueprint_inputs"

// blueprintInputsRecord is the read side of the stored shape. Shared data
// round-trips through JSON on its way to disk, so reading goes through JSON
// too and accepts either the struct that was written or the decoded map.
type blueprintInputsRecord struct {
	Fields []struct {
		Label     string `json:"label"`
		Display   string `json:"display"`
		Value     string `json:"value"`
		Type      string `json:"type"`
		IntakeKey string `json:"intake_key"`
	} `json:"fields"`
}

// BlueprintInputsSummary renders the recorded values as "Label: display" pairs
// — e.g. `Tempo: 96 BPM, Time signature: 3/4` — or "" when the workspace was
// not created from a blueprint that asked for any. Unreadable data yields ""
// rather than an error: a prompt is better off missing one line than failing.
func BlueprintInputsSummary(sharedData map[string]any) string {
	raw, ok := sharedData[BlueprintInputsSharedDataKey]
	if !ok || raw == nil {
		return ""
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	var record blueprintInputsRecord
	if err := json.Unmarshal(encoded, &record); err != nil {
		return ""
	}
	parts := make([]string, 0, len(record.Fields))
	for _, field := range record.Fields {
		label := strings.TrimSpace(field.Label)
		display := strings.TrimSpace(field.Display)
		if label == "" || display == "" {
			continue
		}
		parts = append(parts, label+": "+display)
	}
	return strings.Join(parts, ", ")
}

// BlueprintIntakeInputSummary returns only text and URL setup answers. Intake
// prompts place this host-recorded data inside the same untrusted boundary as a
// source; number/select answers remain a scaffolding concern.
func BlueprintIntakeURLs(sharedData map[string]any, intakeKey string) []string {
	raw, ok := sharedData[BlueprintInputsSharedDataKey]
	if !ok || raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var record blueprintInputsRecord
	if json.Unmarshal(encoded, &record) != nil {
		return nil
	}
	urls := make([]string, 0)
	for _, field := range record.Fields {
		if field.Type == "url" && field.IntakeKey == intakeKey && strings.TrimSpace(field.Value) != "" {
			urls = append(urls, field.Value)
		}
	}
	return urls
}

func BlueprintIntakeInputSummary(sharedData map[string]any) string {
	raw, ok := sharedData[BlueprintInputsSharedDataKey]
	if !ok || raw == nil {
		return ""
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	var record blueprintInputsRecord
	if err := json.Unmarshal(encoded, &record); err != nil {
		return ""
	}
	parts := make([]string, 0, len(record.Fields))
	for _, field := range record.Fields {
		if field.Type != "text" && field.Type != "url" {
			continue
		}
		label, value := strings.TrimSpace(field.Label), strings.TrimSpace(field.Value)
		if label != "" && value != "" {
			parts = append(parts, label+": "+value)
		}
	}
	return strings.Join(parts, "\n")
}
