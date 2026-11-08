package rosterstaffing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/types"
)

// TeamIntent is private material inside an existing owner's review/operation,
// never an API input or standalone authorization. The enclosing owner binds its
// user, exact destination (or proposed Home key), declaration/provider revision,
// and review identity. Consumers must revalidate those facts and these sources
// before effects; a well-formed intent is not evidence that they still match.
type TeamIntent struct {
	Version int          `json:"version"`
	Roles   []RoleIntent `json:"roles"`
}

// RoleIntent names one exact action. Assign preserves the profile and adds no
// grants. Create uses the reviewed configuration and name, not a freshly chosen
// default or suffix. Source/capability witnesses stay inside the review owner.
type RoleIntent struct {
	RoleID            string `json:"role_id"`
	Scope             string `json:"scope"`
	Mode              string `json:"mode"`
	Name              string `json:"name"`
	SourceWitness     string `json:"source_witness,omitempty"`
	CapabilityWitness string `json:"capability_witness,omitempty"`
	// PreviousChoiceDigest is the last canonical staffing outcome for this
	// slot when reviewed, empty for a new slot. It fences clear/re-fill replay.
	PreviousChoiceDigest string                 `json:"previous_choice_digest,omitempty"`
	Provider             string                 `json:"provider,omitempty"`
	Model                string                 `json:"model,omitempty"`
	ReasoningEffort      string                 `json:"reasoning_effort,omitempty"`
	Appearance           *types.AgentAppearance `json:"appearance,omitempty"`
}

var ErrInvalidIntent = errors.New("staffing intent is invalid; review the team again")

func (i *TeamIntent) Clone() *TeamIntent {
	if i == nil {
		return nil
	}
	copy := *i
	copy.Roles = append([]RoleIntent(nil), i.Roles...)
	for n := range copy.Roles {
		copy.Roles[n].Appearance = i.Roles[n].Appearance.Clone()
	}
	return &copy
}

func (i *TeamIntent) Validate() error {
	if i == nil || i.Version != 1 || len(i.Roles) == 0 || len(i.Roles) > MaxRoles {
		return ErrInvalidIntent
	}
	roles, names := map[string]bool{}, map[string]bool{}
	for _, role := range i.Roles {
		if !identifier.MatchString(role.RoleID) || len(role.RoleID) > 80 || roles[role.RoleID] ||
			(role.Scope != "home" && role.Scope != "project" && role.Scope != "workspace") ||
			role.Name == "" || !intentText(role.Name, 120) ||
			strings.ContainsAny(role.Name, "/\\") || role.Name == "." || role.Name == ".." {
			return ErrInvalidIntent
		}
		if role.PreviousChoiceDigest != "" && !reviewDigest(role.PreviousChoiceDigest) {
			return ErrInvalidIntent
		}
		key := strings.ToLower(role.Name)
		if names[key] {
			return ErrInvalidIntent
		}
		roles[role.RoleID], names[key] = true, true
		switch role.Mode {
		case "assign":
			if !intentWitness(role.SourceWitness) ||
				(role.CapabilityWitness != "" && !intentWitness(role.CapabilityWitness)) ||
				role.Provider != "" || role.Model != "" || role.ReasoningEffort != "" || role.Appearance != nil {
				return ErrInvalidIntent
			}
		case "create":
			// Empty configuration is an honest needs-model plan, not permission
			// for an executor to resolve an undisclosed new default.
			if role.SourceWitness != "" || role.CapabilityWitness != "" ||
				(role.Provider == "") != (role.Model == "") ||
				!intentText(role.Provider, 80) || !intentText(role.Model, 120) || !intentText(role.ReasoningEffort, 40) ||
				strings.ContainsAny(role.Provider+role.ReasoningEffort, "/\\") ||
				strings.HasPrefix(role.Model, "/") || strings.HasPrefix(role.Model, "~") || strings.Contains(role.Model, "\\") {
				return ErrInvalidIntent
			}
			if role.Appearance != nil {
				if role.Appearance.Uploaded != nil || (role.Appearance.Mode != types.AppearanceModeGenerated && role.Appearance.Mode != types.AppearanceModeCharacter) {
					return ErrInvalidIntent
				}
				if generated := role.Appearance.Generated; generated != nil {
					if _, ok := types.NormalizeAppearanceColor(generated.Color); !ok || len(generated.Color) > 7 {
						return ErrInvalidIntent
					}
				}
				if character := role.Appearance.Character; character != nil {
					if !safeIdentifier(character.CatalogID) || character.CatalogVersion < 0 {
						return ErrInvalidIntent
					}
				}
				if role.Appearance.Mode == types.AppearanceModeCharacter && role.Appearance.Character == nil {
					return ErrInvalidIntent
				}
			}
		default:
			return ErrInvalidIntent
		}
	}
	return nil
}

// This is an exact private choice, not ranking input. In particular, legitimate
// model IDs may contain a slash and profile names may contain words that the
// more conservative model-evidence redactor omits.
func intentText(value string, limit int) bool {
	if strings.TrimSpace(value) != value || !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit {
		return false
	}
	return strings.IndexFunc(value, unicode.IsControl) < 0
}

// StepDigest identifies one reviewed consequence on its actual destination.
// The canonical role owner records it with the binding; it grants no authority
// by itself and must never be taken from a client as proof of prior approval.
func (role RoleIntent) StepDigest(parentReviewDigest, workspaceID string) string {
	if !reviewDigest(parentReviewDigest) || !identifier.MatchString(workspaceID) ||
		(&TeamIntent{Version: 1, Roles: []RoleIntent{role}}).Validate() != nil {
		return ""
	}
	data, err := json.Marshal(struct {
		Domain string     `json:"domain"`
		Review string     `json:"review"`
		Target string     `json:"target"`
		Role   RoleIntent `json:"role"`
	}{"reviewed-staffing-step-v1", parentReviewDigest, workspaceID, role})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func reviewDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func intentWitness(value string) bool {
	// Opaque: file-backed sources currently issue UUIDs, while a composite
	// wraps those in a digest. Shape validation must not assume either is an
	// enduring identity format; only the source owner can validate a match.
	return identifier.MatchString(value)
}
