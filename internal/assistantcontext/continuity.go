package assistantcontext

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/publicread"
	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// Continuity is derived, disposable conversation data. It is never a transcript,
// remembered fact, current evidence, instruction or action approval.
const (
	RecapVersion             = 1
	RecapItems               = 16
	RecapRunes               = 4000
	RecapBytes               = 16000
	RecapQuoteRunes          = 400
	ContextRecentMessages    = 40
	ContextRecentRunes       = 16000
	ContextMessageRunes      = 6000
	ContextBatchMessages     = 32
	ContextBatchMessageRunes = 1000
	ContextBatchRunes        = 12000
	ContextOlderMessages     = 128
)

var ErrContinuity = errors.New("invalid conversation continuity")
var recapAuthority = regexp.MustCompile(`(?i)(\b(approved|approval|confirmed|confirmation|authorize|authorized|authorization|execute|executing)\b|\bi approve\b|\bgo ahead\b|\b(sudo|curl|wget|npx|npm install|pip install|osascript|bash|zsh|powershell)\b|\b(rm|chmod|chown|eval|exec|python3?|node|sh|git)\s+[./a-z-]|\$\(|\x60|&&|[\x00-\x08\x0b\x0c\x0e-\x1f])`)
var recapCommand = regexp.MustCompile(`(?i)^(please\s+)?(install|enable|trust|grant|bind|publish|send|run|execute|create|delete)\b`)
var recapCitation = regexp.MustCompile(`\[S[1-9][0-9]*\]`)

type RecapSource struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Imported bool   `json:"imported,omitempty"`
	Content  string `json:"content"`
}

type RecapItem struct {
	Kind      string `json:"kind"`
	MessageID string `json:"message_id"`
	Quote     string `json:"quote"`
}

type ConversationRecap struct {
	Version int         `json:"version"`
	Items   []RecapItem `json:"items"`
}

// ContextPin belongs to a canonical read, not browser/model input. Epoch changes
// on source edits/deletes/imports. Appending a local turn preserves old recap
// eligibility but invalidates an in-flight write through Revision/HighWater.
type ContextPin struct {
	Revision   string
	Epoch      int64
	HighWater  int64
	Through    int64
	Generation int64
}

func SafeRecapQuote(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "yes", "yes.", "yes please", "ok", "okay", "sure", "do it", "do it.":
		return false
	}
	return strings.TrimSpace(text) != "" && utf8.ValidString(text) && utf8.RuneCountInString(text) <= RecapQuoteRunes &&
		!recapAuthority.MatchString(text) && !recapCommand.MatchString(strings.TrimSpace(text)) && !recapCitation.MatchString(text) &&
		!sensitive.ContainsSecretLikeText(text) && !publicread.ContainsCredentialMaterial(text)
}

func validRecapKind(kind, role string, imported bool) bool {
	if imported {
		return kind == "imported_history"
	}
	switch kind {
	case "user_goal", "user_constraint", "user_correction":
		return role == "user"
	case "tentative_option", "historical_finding":
		return role == "assistant"
	case "unresolved_question":
		return role == "user" || role == "assistant"
	}
	return false
}

// CompleteRecapQuote prevents dropping a sentence's negation or qualifier.
// A model may select complete sentences/lines, never an interior fragment.
func CompleteRecapQuote(source, quote string) bool {
	if quote == "" {
		return false
	}
	for offset := 0; offset <= len(source)-len(quote); {
		index := strings.Index(source[offset:], quote)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(quote)
		before, after := source[:start], source[end:]
		prefix := strings.TrimRight(before, " \t\r")
		startOK := start == 0 || strings.HasSuffix(before, "\n") || strings.HasSuffix(before, "。") || strings.HasSuffix(before, "！") || strings.HasSuffix(before, "？") || (len(prefix) > 0 && len(prefix) < len(before) && strings.ContainsAny(prefix[len(prefix)-1:], ".!?"))
		last := quote[len(quote)-1:]
		endOK := end == len(source) || strings.HasPrefix(after, "\n") || strings.HasSuffix(quote, "。") || strings.HasSuffix(quote, "！") || strings.HasSuffix(quote, "？") || (strings.ContainsAny(last, ".!?") && strings.ContainsAny(after[:1], " \t\r\n"))
		if startOK && endOK {
			return true
		}
		offset = start + 1
	}
	return false
}

// ValidateRecap permits only exact source quotes and unchanged prior items. The
// model selects relevance/categories, but cannot invent facts or change roles.
func ValidateRecap(value *ConversationRecap, sources []RecapSource, prior *ConversationRecap) error {
	if value == nil || value.Version != RecapVersion || value.Items == nil || len(value.Items) > RecapItems {
		return ErrContinuity
	}
	byID := make(map[string]RecapSource, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	seen := map[RecapItem]bool{}
	for _, item := range value.Items {
		if item.MessageID == "" || len(item.MessageID) > 128 || !SafeRecapQuote(item.Quote) || seen[item] {
			return ErrContinuity
		}
		seen[item] = true
		switch item.Kind {
		case "user_goal", "user_constraint", "user_correction", "tentative_option", "historical_finding", "unresolved_question", "imported_history":
		default:
			return ErrContinuity
		}
		if source, ok := byID[item.MessageID]; ok {
			if !validRecapKind(item.Kind, source.Role, source.Imported) || !CompleteRecapQuote(source.Content, item.Quote) {
				return ErrContinuity
			}
			continue
		}
		found := false
		if prior != nil {
			for _, old := range prior.Items {
				if old == item {
					found = true
					break
				}
			}
		}
		if !found {
			return ErrContinuity
		}
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > RecapBytes || utf8.RuneCount(data) > RecapRunes {
		return ErrContinuity
	}
	return nil
}

func DecodeRecap(data string) (*ConversationRecap, error) {
	if len(data) > RecapBytes {
		return nil, ErrContinuity
	}
	var value ConversationRecap
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil {
		return nil, ErrContinuity
	}
	// Validate against itself only for persisted shape. Canonical eligibility is
	// separately pinned to owner, mutation epoch and covered message range.
	if ValidateRecap(&value, nil, &value) != nil {
		return nil, ErrContinuity
	}
	canonical, _ := json.Marshal(value)
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) || len(canonical) > RecapBytes {
		return nil, ErrContinuity
	}
	return &value, nil
}
