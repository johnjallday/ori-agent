package filejanitor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// ReviewGroupRuleVersion changes whenever primary membership rules change.
const ReviewGroupRuleVersion = 3
const ReviewGroupSampleLimit = 3

// ReviewGroup is a bounded saved proposal, never a filesystem inventory or an
// authorization. Candidate IDs are exposed only by the paged member endpoint.
type ReviewGroup struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Label       string   `json:"label"`
	Reason      string   `json:"reason"`
	Question    string   `json:"question,omitempty"`
	Category    Category `json:"category,omitempty"`
	Destination string   `json:"destination,omitempty"`
	Count       int      `json:"count"`
	Samples     []string `json:"samples"`
}

// ReviewGroupCounts partitions candidates exclusively. Ineligible counts saved
// scan observations separately; they never contribute to Candidates.
type ReviewGroupCounts struct {
	Candidates int `json:"candidates"`
	Filing     int `json:"filing"`
	Questions  int `json:"questions"`
	Kept       int `json:"kept"`
	Applied    int `json:"applied"`
	Stale      int `json:"stale"`
	Failed     int `json:"failed"`
	Applying   int `json:"applying"`
	Unknown    int `json:"unknown"`
	Ineligible int `json:"ineligible"`
}

// ReviewGroups contains fixed-vocabulary cards and whole-batch accounting.
// Private member indexes are derived once, not serialized into the card read.
type ReviewGroups struct {
	BatchID     string            `json:"batch_id"`
	Revision    string            `json:"revision"`
	RuleVersion int               `json:"rule_version"`
	Counts      ReviewGroupCounts `json:"counts"`
	Groups      []ReviewGroup     `json:"groups"`
	members     map[string][]ReviewGroupMember
}

type ReviewGroupMember struct {
	Candidate JanitorCandidate
	Cautions  []string
}

// Members returns a copy of this saved projection's members in batch order.
// HTTP callers must page the result before constructing candidate DTOs.
func (g ReviewGroups) Members(groupID string) ([]ReviewGroupMember, bool) {
	members, found := g.members[groupID]
	return append([]ReviewGroupMember(nil), members...), found
}

type groupDefinition struct {
	key, label, reason string
	category           Category
}

var questionGroups = []groupDefinition{
	{"sensitive", "Sensitive-looking names", "A filename contains a sensitive-looking word. This is not a sensitivity check; decide where these files belong.", ""},
	{"versions", "Possible copies or versions", "An explicit copy/version suffix shares a name stem and extension with another saved candidate. Contents and newest versions have not been compared.", ""},
	{"audio", "Audio and related material", "Audio metadata, a music-project extension, or another file sharing a saved audio filename stem suggests a question. These are not verified projects or duplicates; archives were not opened.", ""},
	{"unresolved", "Choose a filing category", "The saved classification needs review. Choose a category explicitly before filing; the proposal is not a verified content description.", ""},
	{"unmatched", "Other pending files", "These saved proposals lack a supported category or usable bounded name metadata. Review individual details rather than guessing a destination.", ""},
}

// GroupReview projects one complete canonical saved batch with no IO. scope is
// an opaque digest of the validated server-side owner/root/install identity;
// callers must not use a label, a timestamp alone, or a client-provided scope.
func GroupReview(batch JanitorBatch, candidates []JanitorCandidate, scope string) (ReviewGroups, error) {
	out := ReviewGroups{
		BatchID: batch.ID, RuleVersion: ReviewGroupRuleVersion,
		Groups: []ReviewGroup{}, members: make(map[string][]ReviewGroupMember),
		Counts: ReviewGroupCounts{Candidates: len(candidates), Ineligible: len(batch.Ineligible)},
	}
	if batch.ID == "" || batch.WorkspaceID == "" || scope == "" || len(candidates) != len(batch.CandidateIDs) {
		return ReviewGroups{}, ErrReviewUnavailable
	}
	byID := make(map[string]JanitorCandidate, len(candidates))
	families := make(map[string]versionEvidence, len(candidates))
	audioStems := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return ReviewGroups{}, ErrReviewUnavailable
		}
		if candidate.ID == "" || candidate.WorkspaceID != batch.WorkspaceID || candidate.BatchID != batch.ID {
			return ReviewGroups{}, ErrReviewUnavailable
		}
		if _, duplicate := byID[candidate.ID]; duplicate {
			return ReviewGroups{}, ErrReviewUnavailable
		}
		byID[candidate.ID] = candidate
		if proposalPending(candidate) {
			if audioReviewCandidate(candidate) && len(candidate.Name) <= 1024 {
				if stem := reviewNameStem(candidate.Name); stem != "" {
					audioStems[stem] = true
				}
			}
			if family, marked := versionFamily(candidate.Name); family != "" {
				evidence := families[family]
				evidence.count++
				evidence.marked = evidence.marked || marked
				families[family] = evidence
			}
		}
	}

	// Hash structured values, not delimiter-joined untrusted names. Destination
	// edits and unrelated approval/UpdatedAt churn deliberately do not occur here.
	hash := sha256.New()
	encode := json.NewEncoder(hash)
	if err := encode.Encode([]any{ReviewGroupRuleVersion, scope, batch.WorkspaceID, batch.ID, batch.RootID, batch.CapabilityInstalledAt}); err != nil {
		return ReviewGroups{}, fmt.Errorf("encode group revision: %w", err)
	}
	definitions := append([]groupDefinition(nil), questionGroups...)
	for _, category := range CategoryRegistry {
		definitions = append(definitions, groupDefinition{
			key: "filing-" + string(category.ID), label: category.Label,
			reason: "Grouped by the saved proposed category. Open file details for the actual classification reason and provenance.", category: category.ID,
		})
	}
	cards := make(map[string]*ReviewGroup, len(definitions))
	for _, definition := range definitions {
		card := &ReviewGroup{ID: reviewGroupID(definition.key), Kind: "question", Label: definition.label, Reason: definition.reason, Category: definition.category, Samples: []string{}}
		if definition.category != "" {
			card.Kind = "filing"
			category, _ := LookupCategory(string(definition.category))
			card.Destination = path.Join(DefaultFilingRootName, category.FolderName)
		}
		card.Question = reviewQuestion(definition.key)
		cards[definition.key] = card
	}
	seen := make(map[string]bool, len(candidates))
	for _, id := range batch.CandidateIDs {
		candidate, found := byID[id]
		if !found || seen[id] {
			return ReviewGroups{}, ErrReviewUnavailable
		}
		seen[id] = true
		key, cautions := primaryReviewGroup(candidate, families, audioStems)
		if err := encode.Encode([]any{candidate.ID, candidate.Fingerprint.Key(), candidate.Category, candidate.NeedsReview, candidate.Confidence, candidate.Classifier, candidate.State, candidate.Decision == DecisionTrash, key, cautions}); err != nil {
			return ReviewGroups{}, fmt.Errorf("encode group revision: %w", err)
		}
		if !proposalPending(candidate) {
			switch candidate.State {
			case CandidateSkipped:
				out.Counts.Kept++
			case CandidateApplied:
				out.Counts.Applied++
			case CandidateStale:
				out.Counts.Stale++
			case CandidateFailed:
				out.Counts.Failed++
			case CandidateApplying:
				out.Counts.Applying++
			default:
				out.Counts.Unknown++
			}
			continue
		}
		card := cards[key]
		card.Count++
		if len(card.Samples) < ReviewGroupSampleLimit {
			// Re-render the raw name rather than trusting a legacy DisplayName.
			card.Samples = append(card.Samples, DisplayFileName(candidate.Name))
		}
		out.members[card.ID] = append(out.members[card.ID], ReviewGroupMember{Candidate: candidate, Cautions: cautions})
		if card.Kind == "filing" {
			out.Counts.Filing++
		} else {
			out.Counts.Questions++
		}
	}
	for _, definition := range definitions {
		if card := cards[definition.key]; card.Count > 0 {
			out.Groups = append(out.Groups, *card)
		}
	}
	out.Revision = hex.EncodeToString(hash.Sum(nil))
	return out, nil
}

func proposalPending(candidate JanitorCandidate) bool {
	return candidate.State == CandidatePending || candidate.State == CandidateApproved
}

func reviewGroupID(key string) string {
	sum := sha256.Sum256([]byte("file-janitor-review-group-v1:" + key))
	return "group-" + hex.EncodeToString(sum[:12])
}

func reviewQuestion(key string) string {
	switch key {
	case "sensitive":
		return "Where should these sensitive-looking files go?"
	case "versions":
		return "Which possible copies or versions do you want to file?"
	case "audio":
		return "Keep this audio and related material here?"
	case "unresolved":
		return "Where should these uncertain files go?"
	case "unmatched":
		return "How should these unmatched files be handled?"
	}
	return ""
}

type versionEvidence struct {
	count  int
	marked bool
}

func primaryReviewGroup(candidate JanitorCandidate, families map[string]versionEvidence, audioStems map[string]bool) (string, []string) {
	if len(candidate.Name) > 1024 {
		return "unmatched", []string{"unmatched"}
	}
	var cautions []string
	if sensitiveReviewName(candidate.Name) {
		cautions = append(cautions, "sensitive")
	}
	if family, _ := versionFamily(candidate.Name); family != "" && families[family].count > 1 && families[family].marked {
		cautions = append(cautions, "versions")
	}
	if audioReviewCandidate(candidate) || relatedAudioCandidate(candidate, audioStems) {
		cautions = append(cautions, "audio")
	}
	definition, categoryErr := LookupCategory(string(candidate.Category))
	if candidate.NeedsReview || (categoryErr == nil && definition.ID == CategoryOther) || candidate.Confidence == ConfidenceLow {
		cautions = append(cautions, "unresolved")
	}
	if len(cautions) > 0 {
		return cautions[0], cautions
	}
	if categoryErr == nil {
		return "filing-" + string(definition.ID), nil
	}
	return "unmatched", []string{"unmatched"}
}

func sensitiveReviewName(name string) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		switch token {
		case "passport", "password", "passwords", "medical", "tax", "confidential", "ssn", "secret", "secrets":
			return true
		}
	}
	return false
}

var versionSuffix = regexp.MustCompile(`(?i)(?:[ _.-]+(?:v[0-9]{1,4}|version[ _.-]*[0-9]{1,4}|copy(?:[ _.-]*[0-9]{1,4})?)| ?\([0-9]{1,4}\))$`)

func versionFamily(name string) (string, bool) {
	if len(name) > 1024 {
		return "", false
	}
	name = strings.ToLower(name)
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	base := versionSuffix.ReplaceAllString(stem, "")
	marked := base != stem
	base = strings.TrimSpace(base)
	if base == "" {
		return "", false
	}
	return base + "\x00" + extension, marked
}

func reviewNameStem(name string) string {
	stem := strings.TrimSuffix(strings.ToLower(name), strings.ToLower(filepath.Ext(name)))
	return strings.TrimSpace(versionSuffix.ReplaceAllString(stem, ""))
}

func relatedAudioCandidate(candidate JanitorCandidate, stems map[string]bool) bool {
	if stem := reviewNameStem(candidate.Name); stem == "" || !stems[stem] {
		return false
	}
	return true // exact saved stem only; no project-format or archive knowledge
}

func audioReviewCandidate(candidate JanitorCandidate) bool {
	if strings.EqualFold(strings.TrimSpace(string(candidate.Category)), string(CategoryAudio)) || strings.HasPrefix(strings.ToLower(candidate.MIMEType), "audio/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(candidate.Name)) {
	case ".als", ".logicx", ".band", ".flp", ".ptx", ".aup", ".aup3", ".cue":
		return true
	}
	return false
}
