package rosterstaffing

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// Advice is bounded private advisory state inside an existing draft or review
// owner. It is never a selected team. Public removes source/capability witnesses
// before presentation; neither form authorizes assignment.
type Advice struct {
	Version          int          `json:"version"`
	ContextVersion   int64        `json:"context_version"`
	TemplateID       string       `json:"template_id"`
	TemplateRevision string       `json:"template_revision"`
	State            string       `json:"state"`
	Coverage         Coverage     `json:"coverage"`
	Roles            []RoleAdvice `json:"roles,omitempty"`
}

type RoleAdvice struct {
	RoleID  string         `json:"role_id"`
	Scope   string         `json:"scope"`
	State   string         `json:"state"`
	Choices []AdviceChoice `json:"choices,omitempty"`
}

type AdviceChoice struct {
	Handle            string     `json:"handle"`
	Name              string     `json:"name"`
	Source            string     `json:"source"`
	Configured        bool       `json:"configured"`
	Reasons           []Evidence `json:"reasons"`
	Assignments       []Evidence `json:"assignments,omitempty"`
	Gaps              []string   `json:"gaps,omitempty"`
	SourceWitness     string     `json:"source_witness,omitempty"`
	CapabilityWitness string     `json:"capability_witness,omitempty"`
}

var ErrInvalidAdvice = errors.New("staffing recommendation is invalid")

func (s *Advice) Clone() *Advice {
	if s == nil {
		return nil
	}
	out := *s
	out.Roles = append([]RoleAdvice(nil), s.Roles...)
	for i := range out.Roles {
		out.Roles[i].Choices = append([]AdviceChoice(nil), s.Roles[i].Choices...)
		for j := range out.Roles[i].Choices {
			choice := &out.Roles[i].Choices[j]
			choice.Reasons = append([]Evidence(nil), choice.Reasons...)
			choice.Assignments = append([]Evidence(nil), choice.Assignments...)
			choice.Gaps = append([]string(nil), choice.Gaps...)
		}
	}
	return &out
}

func (s *Advice) Public() *Advice {
	out := s.Clone()
	if out != nil {
		for i := range out.Roles {
			for j := range out.Roles[i].Choices {
				out.Roles[i].Choices[j].SourceWitness = ""
				out.Roles[i].Choices[j].CapabilityWitness = ""
			}
		}
	}
	return out
}

// Validate is for private persisted state, not its stripped public projection.
// V1 is the original builder shape; only V2 may carry witnessed skill evidence.
func (s *Advice) Validate(contextVersion int64) error {
	if s == nil {
		return nil
	}
	if (s.Version != 1 && s.Version != 2) || s.ContextVersion < 0 || s.ContextVersion > contextVersion ||
		!adviceText(s.TemplateID, 160) || !adviceText(s.TemplateRevision, 64) || len(s.Roles) > MaxRoles || !adviceState(s.State) ||
		s.Coverage.Inspected < 0 || s.Coverage.Included < 0 || s.Coverage.Included > MaxCandidates || s.Coverage.Included > s.Coverage.Inspected {
		return ErrInvalidAdvice
	}
	roles := map[string]bool{}
	for _, role := range s.Roles {
		if !adviceOpaque(role.RoleID) || roles[role.RoleID] || !adviceState(role.State) ||
			(role.Scope != "home" && role.Scope != "project" && role.Scope != "workspace") || len(role.Choices) > 1+MaxAlternatives {
			return ErrInvalidAdvice
		}
		roles[role.RoleID] = true
		for _, choice := range role.Choices {
			if !adviceOpaque(choice.Handle) || !adviceOpaque(choice.SourceWitness) || !adviceText(choice.Name, 120) ||
				(choice.Source != "root" && choice.Source != "legacy") || len(choice.Reasons) == 0 || len(choice.Reasons) > 3 || len(choice.Gaps) > 8 || len(choice.Assignments) > 8 ||
				(choice.CapabilityWitness != "" && (s.Version != 2 || !reviewDigest(choice.CapabilityWitness))) {
				return ErrInvalidAdvice
			}
			for _, reason := range choice.Reasons {
				if !adviceOpaque(reason.ID) || !adviceText(reason.Value, MaxPurposeRunes) {
					return ErrInvalidAdvice
				}
				switch reason.Kind {
				case "declared_purpose", "declared_role", "declared_capability", "current_assignment":
				case "enabled_skill":
					if s.Version != 2 || choice.CapabilityWitness == "" {
						return ErrInvalidAdvice
					}
				default:
					return ErrInvalidAdvice
				}
			}
			for _, assignment := range choice.Assignments {
				if assignment.Kind != "current_assignment" || !adviceOpaque(assignment.ID) || assignment.Value == "" || safeText(assignment.Value, MaxPurposeRunes) != assignment.Value {
					return ErrInvalidAdvice
				}
			}
			for _, gap := range choice.Gaps {
				if !adviceOpaque(gap) {
					return ErrInvalidAdvice
				}
			}
		}
	}
	data, err := json.Marshal(s)
	if err != nil || len(data) > 128*1024 {
		return ErrInvalidAdvice
	}
	return nil
}

func adviceText(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit && !strings.ContainsRune(value, 0)
}

func adviceOpaque(value string) bool {
	return value != "" && adviceText(value, 128) && !strings.ContainsAny(value, "/\\\r\n\t ")
}

func adviceState(state string) bool {
	switch state {
	case Recommended, NeedsChoice, NoSuitableMatch, Unavailable, Stale:
		return true
	default:
		return false
	}
}
