package workspaceplan

import (
	"fmt"
	"regexp"
	"strings"
)

// Keeping credentials out of Plan content, and out of what Plan history shows
// (FR-170, FR-171).
//
// A Plan is durable, versioned, exported into workspace documents, and shown in
// review. A token that reaches it is not a transient leak — it is written down
// in several places at once and survives every one of them. So content carrying
// a credential is REFUSED at validation rather than stored and redacted later:
// once it is in an immutable version, redaction can only hide it, not remove
// it.
//
// Activity reasons are different. They are prose about what happened, they are
// not approval-relevant, and refusing one would block a legitimate state change
// over its explanation. Those are redacted on the way in instead.
//
// The detectors below match credential SHAPES, never words. "Rotate the
// database password" is an ordinary plan step; `password=hunter2` is a leak.
// Matching the word would refuse the first, which teaches people to work around
// the check.

// credentialPattern is one credential shape, with a name the user sees.
type credentialPattern struct {
	name    string
	pattern *regexp.Regexp
}

// credentialPatterns covers the shapes worth refusing outright.
//
// It is deliberately not exhaustive — no regex set is — and it is not the only
// protection. It catches the accidental paste, which is how credentials
// actually reach a plan; a determined encoding is not the threat model here.
var credentialPatterns = []credentialPattern{
	{"an OpenAI-style API key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`)},
	{"a GitHub token", regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`)},
	{"a GitHub fine-grained token", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`)},
	{"an AWS access key ID", regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"a Slack token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{"a Google API key", regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{35}\b`)},
	{"a private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"a bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{20,}=*`)},
	// An assignment is the shape that matters: the word alone is fine, a word
	// followed by a value is not.
	{"an inline credential", regexp.MustCompile(
		`(?i)\b(password|passwd|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)\s*[:=]\s*\S{6,}`)},
}

// CredentialFinding is one credential-shaped string found in Plan content.
type CredentialFinding struct {
	// Field is where it was found, in the same dotted form validation issues
	// use, so the UI can point at it.
	Field string
	// Name describes the shape, never the value. Echoing the credential back
	// in an error message would write it to a second place.
	Name string
}

// FindCredentials reports credential shapes in Plan content.
//
// Only fields a person or model writes free text into are scanned. IDs, enum
// values, and booleans cannot carry a pasted key, and scanning them would cost
// time on every validation for nothing.
func FindCredentials(objective string, content PlanContent) []CredentialFinding {
	var findings []CredentialFinding

	scan := func(field, text string) {
		for _, name := range scanText(text) {
			findings = append(findings, CredentialFinding{Field: field, Name: name})
		}
	}

	scan("objective", objective)
	scan("rationale", content.Rationale)
	scan("explanation", content.Explanation)
	for index, value := range content.InScope {
		scan(fieldPath("in_scope", index), value)
	}
	for index, value := range content.NonGoals {
		scan(fieldPath("non_goals", index), value)
	}

	for groupIndex, group := range content.Groups {
		base := fieldPath("groups", groupIndex)
		scan(base+".title", group.Title)
		scan(base+".outcome", group.Outcome)
		scan(base+".notes", group.Notes)
		for itemIndex, item := range group.Items {
			itemBase := base + "." + fieldPath("items", itemIndex)
			scan(itemBase+".description", item.Description)
			scan(itemBase+".details", item.Details)
			scan(itemBase+".expected_result", item.ExpectedResult)
			scan(itemBase+".reference_url", item.ReferenceURL)
		}
	}

	for index, artifact := range content.Artifacts {
		base := fieldPath("artifacts", index)
		scan(base+".title", artifact.Title)
		scan(base+".description", artifact.Description)
		scan(base+".path", artifact.Path)
	}
	for index, checkpoint := range content.Validations {
		base := fieldPath("validations", index)
		scan(base+".title", checkpoint.Title)
		scan(base+".expectation", checkpoint.Expectation)
	}

	return findings
}

// fieldPath renders an indexed field reference the way validation issues do,
// so the UI can point at the same place for either kind of problem.
func fieldPath(field string, index int) string {
	return fmt.Sprintf("%s[%d]", field, index)
}

// scanText returns the names of every credential shape in one string.
func scanText(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var names []string
	for _, candidate := range credentialPatterns {
		if candidate.pattern.MatchString(text) {
			names = append(names, candidate.name)
		}
	}
	return names
}

// RedactCredentials replaces credential shapes with a marker.
//
// It is used for text that must be KEPT — activity reasons, failure messages —
// where refusing would block a legitimate operation over its explanation. The
// marker names nothing about the value; it exists so a reader knows something
// was removed rather than wondering whether the sentence was always that odd.
func RedactCredentials(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	for _, candidate := range credentialPatterns {
		text = candidate.pattern.ReplaceAllString(text, "[redacted]")
	}
	return text
}

// credentialIssues converts findings into validation issues.
func credentialIssues(findings []CredentialFinding) []ValidationIssue {
	issues := make([]ValidationIssue, 0, len(findings))
	for _, finding := range findings {
		issues = append(issues, ValidationIssue{
			Code:  IssueCredentialInContent,
			Field: finding.Field,
			// The message names the shape and the fix, and never the value.
			Message: "this looks like " + finding.Name +
				". Remove it — a plan is stored, versioned, and shown in review, " +
				"so a credential here is written down in several places at once. " +
				"Reference it by name instead and keep the value in the vault",
		})
	}
	return issues
}
