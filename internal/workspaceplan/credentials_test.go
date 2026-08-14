package workspaceplan

import (
	"strings"
	"testing"
)

func contentWithDescription(description string) PlanContent {
	return PlanContent{
		Execution: ExecutionPolicy{Mode: ExecutionStepThrough},
		Groups: []TaskGroup{{
			ID: "grp-1", Title: "Prepare",
			Items: []TaskItem{{ID: "itm-1", Description: description}},
		}},
	}
}

// --- Detection (FR-170) ----------------------------------------------------

func TestCredentialShapesAreDetected(t *testing.T) {
	cases := map[string]string{
		"openai key":   "Use sk-abcdefghijklmnopqrstuvwxyz012345 to call the API",
		"github token": "Push with ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"fine-grained": "Auth via github_pat_11ABCDEFG0abcdefghij_klmnopqrstuv",
		"aws key":      "Set AKIAIOSFODNN7EXAMPLE in the environment",
		"slack token":  "Post using xoxb-1234567890-abcdefghijkl",
		// Google API keys are AIza plus exactly 35 characters.
		"google key":     "Key AIzaSyD1234567890abcdefghijklmnopqrstuv",
		"bearer":         "Send Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		"private key":    "-----BEGIN RSA PRIVATE KEY-----",
		"inline assign":  "Connect with password=hunter2swordfish",
		"api_key assign": "api_key: abcd1234efgh5678",
	}

	for name, text := range cases {
		findings := FindCredentials("", contentWithDescription(text))
		if len(findings) == 0 {
			t.Errorf("%s was not detected: %q", name, text)
		}
	}
}

// The check matches shapes, not words. Refusing ordinary sentences would teach
// people to phrase around the check, which is worse than not having it.
func TestOrdinaryPlanTextIsNotRefused(t *testing.T) {
	safe := []string{
		"Rotate the database password",
		"Document how the API key is provisioned",
		"Ask the security team about token expiry",
		"Store the client secret in the vault",
		"Review bearer authentication in the gateway",
		"Add a secrets scanning step to CI",
	}

	for _, text := range safe {
		findings := FindCredentials("", contentWithDescription(text))
		if len(findings) > 0 {
			t.Errorf("ordinary text was flagged as a credential: %q (%+v)", text, findings)
		}
	}
}

// Validation refuses the content, so a credential never reaches an immutable
// version where redaction could only hide it.
func TestValidationRefusesContentCarryingACredential(t *testing.T) {
	content := contentWithDescription("Deploy with sk-abcdefghijklmnopqrstuvwxyz012345")
	result := ValidatePlanContent("Ship it", content, ValidationContext{})

	if result.OK() {
		t.Fatal("content carrying an API key passed validation")
	}
	var found bool
	for _, issue := range result.Issues {
		if issue.Code == IssueCredentialInContent {
			found = true
			// The message must never echo the value back — that would write it
			// to a second place.
			if strings.Contains(issue.Message, "sk-abcdefghijklmnopqrstuvwxyz012345") {
				t.Error("the error message repeated the credential")
			}
			if !strings.Contains(issue.Message, "vault") {
				t.Errorf("the message does not say what to do instead: %q", issue.Message)
			}
		}
	}
	if !found {
		t.Errorf("no credential issue was raised: %+v", result.Issues)
	}
}

// The finding points at the field, so the editor can highlight it.
func TestAFindingNamesTheFieldItCameFrom(t *testing.T) {
	findings := FindCredentials("", contentWithDescription("token AKIAIOSFODNN7EXAMPLE"))
	if len(findings) == 0 {
		t.Fatal("nothing detected")
	}
	if !strings.Contains(findings[0].Field, "groups[0]") ||
		!strings.Contains(findings[0].Field, "description") {
		t.Errorf("field = %q, want it to point at the item description", findings[0].Field)
	}
}

// The objective is scanned too: it is the most-read field on the page.
func TestTheObjectiveIsScanned(t *testing.T) {
	findings := FindCredentials("Deploy using ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		contentWithDescription("Something harmless"))
	if len(findings) == 0 {
		t.Fatal("a credential in the objective was not detected")
	}
	if findings[0].Field != "objective" {
		t.Errorf("field = %q, want objective", findings[0].Field)
	}
}

// --- Redaction (FR-171) ----------------------------------------------------

// Activity reasons are kept and redacted rather than refused: blocking a
// legitimate state change over its explanation would be worse than the leak it
// prevents, and the value is removed either way.
func TestActivityReasonsAreRedacted(t *testing.T) {
	plan := &Plan{ID: "plan-1", WorkspaceID: "ws-1", Status: StatusDraft}
	entry := NewStatusChange(plan, StatusInReview, SourceUser, "jj",
		"ready to review, deploy key sk-abcdefghijklmnopqrstuvwxyz012345")

	if strings.Contains(entry.Reason, "sk-abcdefghijklmnopqrstuvwxyz012345") {
		t.Errorf("the activity reason stored a credential: %q", entry.Reason)
	}
	if !strings.Contains(entry.Reason, "[redacted]") {
		t.Errorf("the redaction is invisible to the reader: %q", entry.Reason)
	}
	// The rest of the sentence survives, so the record still explains itself.
	if !strings.Contains(entry.Reason, "ready to review") {
		t.Errorf("redaction destroyed the explanation: %q", entry.Reason)
	}
}

func TestRedactionLeavesOrdinaryTextAlone(t *testing.T) {
	const text = "paused because the branch check failed"
	if got := RedactCredentials(text); got != text {
		t.Errorf("ordinary text was altered: %q", got)
	}
}
