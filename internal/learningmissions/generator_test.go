package learningmissions

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func designFixture(t *testing.T) (ContextSnapshot, ModelConfiguration, string) {
	t.Helper()
	snapshot, err := BuildContext(t.Context(), &contextSource{}, contextOwner(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	model := ModelConfiguration{Provider: "openai", Model: "configured-model", Fingerprint: digest([]byte("model"))}
	raw := `{"candidate_ref":"candidate","structure":"delegate_inspect","version":1,"steps":["scope","execute","review"],"goal":"Draft a workshop welcome","scope":"Use only your supplied text","why":"Your explicit focus is a community workshop","hints":[]}`
	return snapshot, model, raw
}

func TestValidateProposalPinsHostIdentityAndRejectsAuthorityOrUnsupportedCopy(t *testing.T) {
	snapshot, model, raw := designFixture(t)
	d, err := ValidateProposal(raw, snapshot, model, time.Now())
	if err != nil || d.WorkspaceID != "workspace" || d.Incarnation != "incarnation" || !d.Personalized || d.Guidance != snapshot.Data.Guidance || d.ModelFingerprint != model.Fingerprint {
		t.Fatalf("host binding: %+v %v", d, err)
	}
	cases := map[string]string{
		"fabricated candidate":   strings.Replace(raw, `"candidate"`, `"invented"`, 1),
		"changed structure":      strings.Replace(raw, `delegate_inspect`, `repeat_success`, 1),
		"retired version":        strings.Replace(raw, `"version":1`, `"version":2`, 1),
		"unsupported sequence":   strings.Replace(raw, `"execute","review"`, `"review","execute"`, 1),
		"new permission":         strings.Replace(raw, `"scope","execute","review"`, `"scope","grant","review"`, 1),
		"command prose":          strings.Replace(raw, `Draft a workshop welcome`, `Run sudo rm -rf everything`, 1),
		"hostile name echoed":    strings.Replace(raw, `Draft a workshop welcome`, fName(), 1),
		"action URL prose":       strings.Replace(raw, `Use only your supplied text`, `Open https://unsafe.example/action`, 1),
		"completion claim":       strings.TrimSuffix(raw, "}") + `,"completed":true}`,
		"owner":                  strings.TrimSuffix(raw, "}") + `,"owner":"local"}`,
		"workspace":              strings.TrimSuffix(raw, "}") + `,"studio_id":"workspace"}`,
		"original receipt":       strings.TrimSuffix(raw, "}") + `,"original_receipt":"forged"}`,
		"reward":                 strings.TrimSuffix(raw, "}") + `,"reward":100}`,
		"duplicate":              strings.TrimSuffix(raw, "}") + `,"goal":"different"}`,
		"case alias":             strings.Replace(raw, `"goal"`, `"Goal"`, 1),
		"trailing":               raw + `{}`,
		"missing required hints": strings.Replace(raw, `,"hints":[]`, "", 1),
		"oversized":              strings.Repeat("x", MaxResponseBytes+1),
	}
	for name, invalid := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateProposal(invalid, snapshot, model, time.Now()); err == nil {
				t.Fatalf("accepted invalid proposal: %s", invalid)
			}
		})
	}
	snapshot.Data.Guidance = GuidanceBasic
	if _, err := ValidateProposal(strings.Replace(raw, `"hints":[]`, `"hints":["Guided help"]`, 1), snapshot, model, time.Now()); err == nil {
		t.Fatal("model overrode the user's basic support preference")
	}
	snapshot.Data.Candidates = nil
	if _, err := ValidateProposal(raw, snapshot, model, time.Now()); err == nil {
		t.Fatal("model selected a candidate not disclosed in its bounded context")
	}
}

func TestValidateProposalUsesExactCompiledStepsAndHostLineageForAllStructures(t *testing.T) {
	for _, id := range []StructureID{DelegateInspect, ImproveResult, RepeatSuccess} {
		t.Run(string(id), func(t *testing.T) {
			snapshot, model, raw := designFixture(t)
			structure, _ := Lookup(id, 1)
			candidate := snapshot.Bindings["candidate"]
			candidate.Structure, candidate.OriginalReceipt = id, "host-local-receipt"
			snapshot.Bindings["candidate"] = candidate
			snapshot.Data.Candidates[0].Structure, snapshot.Data.Candidates[0].Steps = id, structure.Steps
			var generated GeneratedProposal
			if err := json.Unmarshal([]byte(raw), &generated); err != nil {
				t.Fatal(err)
			}
			generated.Structure, generated.Steps = id, structure.Steps
			encoded, err := json.Marshal(generated)
			if err != nil {
				t.Fatal(err)
			}
			d, err := ValidateProposal(string(encoded), snapshot, model, time.Now())
			if err != nil || d.OriginalReceipt != "host-local-receipt" || d.Structure != id {
				t.Fatalf("lineage: %+v %v", d, err)
			}
		})
	}
}
