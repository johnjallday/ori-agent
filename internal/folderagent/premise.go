package folderagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/johnjallday/ori-agent/internal/folderlook"
	"github.com/johnjallday/ori-agent/internal/foldermap"
)

// ErrPremiseOwed refuses Set it up while the owner has not confirmed what the
// folder is (PRD FR10): a stale tab or a scripted call cannot skip the question.
var ErrPremiseOwed = errors.New("folderagent: confirm what the folder is first")

// Premise view states.
const (
	PremiseAsked = "asked"
)

// PremiseView is the premise on the card (PRD FR1–FR6): the host's sentence
// and, while it is asked, the two answers. Confirmed, it is one plain line;
// answered "something else", the recommendation is withdrawn until a look
// with the owner's own words.
type PremiseView struct {
	// State is PremiseAsked, PremiseConfirmed or PremiseSomethingElse.
	State    string `json:"state"`
	Sentence string `json:"sentence"`
	// Confirm and Other are the two answers' labels while it is asked.
	Confirm string `json:"confirm,omitempty"`
	Other   string `json:"other,omitempty"`
	// Withdrawn is set after "something else" until a look follows it: the
	// card offers only a plain workspace or a look with the owner's words.
	Withdrawn bool `json:"withdrawn,omitempty"`
}

// premiseProvider is the Home provider whose kind the premise names, or ""
// when the host recognised no portfolio a Home can hold (PRD FR1, FR8).
func premiseProvider(rec *Record) string {
	if rec == nil || rec.Map == nil || rec.RootPolicy != folderlook.RootOrdinary || rec.Facts.Portfolio == nil {
		return ""
	}
	return rec.Facts.Portfolio.HomeProviderKey
}

// premiseApplies reports whether this record is asked the premise at all: a
// record made since the question shipped (PRD FR11), with a portfolio, not
// fed from inside the Home that would hold it (PRD FR8).
func premiseApplies(rec *Record) bool {
	key := premiseProvider(rec)
	return key != "" && rec.AsksPremise && rec.FedFromHome != key
}

// currentPremise is the owner's answer for the kind the facts name now; an
// answer given for another kind does not count (PRD FR9).
func currentPremise(rec *Record) *PremiseAnswer {
	if !premiseApplies(rec) || rec.Premise == nil || rec.Premise.Provider != premiseProvider(rec) {
		return nil
	}
	return rec.Premise
}

// premiseOwed reports whether the report waits on the owner's answer.
func premiseOwed(rec *Record) bool {
	return premiseApplies(rec) && currentPremise(rec) == nil
}

// premiseWithdrawn reports whether the owner said "something else" and no
// look has followed: nothing is recommended until one does (PRD FR6).
func premiseWithdrawn(rec *Record) bool {
	p := currentPremise(rec)
	return p != nil && p.Answer == PremiseSomethingElse && !p.Relooked
}

// premiseDeclined reports whether the owner said "something else" for the
// kind the facts name now: the default plan then leaves out the Home and
// every install (PRD FR7).
func premiseDeclined(rec *Record) bool {
	p := currentPremise(rec)
	return p != nil && p.Answer == PremiseSomethingElse
}

// premiseView is the card's premise, nil when the record is not asked it.
func premiseView(rec *Record) *PremiseView {
	if !premiseApplies(rec) {
		return nil
	}
	p := rec.Facts.Portfolio
	kind := premiseKind(p.HomeName)
	counts := joinAnd(foldermap.FormatCounts(rec.Facts))
	answer := currentPremise(rec)
	switch {
	case answer == nil:
		return &PremiseView{State: PremiseAsked,
			Sentence: fmt.Sprintf("This looks like %s %s folder: %s.", article(kind), kind, counts),
			Confirm:  "Manage them as " + foldermap.ShapeNoun(p.Shape, 2),
			Other:    "It's something else"}
	case answer.Answer == PremiseConfirmed:
		return &PremiseView{State: PremiseConfirmed,
			Sentence: fmt.Sprintf("%s %s folder: %s.", capitalize(article(kind)), kind, counts)}
	}
	return &PremiseView{State: PremiseSomethingElse, Sentence: "Nothing is installed or created.", Withdrawn: !answer.Relooked}
}

// premiseKind is the kind word from the Home the capability table offers:
// its name without "Home", lower-cased ("music production"). Host code never
// names a kind itself.
func premiseKind(homeName string) string {
	kind := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(homeName), "Home"))
	if kind == "" {
		return "project"
	}
	return strings.ToLower(kind)
}

func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", unicode.ToLower(rune(word[0]))) {
		return "an"
	}
	return "a"
}

func capitalize(word string) string {
	if word == "" {
		return word
	}
	return strings.ToUpper(word[:1]) + word[1:]
}

// joinAnd joins parts as "a", "a and b", or "a, b, and c".
func joinAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
}

// AnswerPremise records the owner's answer to the premise (PRD FR5, FR6): the
// answer, the kind it was given for, and when. The same answer again is a
// no-op; any other answer once one is recorded, or an answer with nothing
// asked, is refused.
func (s *Service) AnswerPremise(ctx context.Context, userID, workspaceID, answer string) (View, error) {
	if answer != PremiseConfirmed && answer != PremiseSomethingElse {
		return View{}, fmt.Errorf("%w: %s", errValidationPremise, answer)
	}
	if _, err := s.Record(userID, workspaceID); err != nil {
		return View{}, err
	}
	rec, err := s.deps.Store.Update(workspaceID, func(r *Record) error {
		current := s.effectiveRecord(r)
		if current.State != StateReported && current.State != StateKept {
			return ErrWrongState
		}
		if !premiseApplies(current) {
			return ErrWrongState
		}
		if p := currentPremise(current); p != nil {
			if p.Answer == answer {
				return errPremiseUnchanged
			}
			return ErrWrongState
		}
		r.Premise = &PremiseAnswer{Answer: answer, Provider: premiseProvider(current), At: s.deps.Now().UTC()}
		return nil
	})
	if errors.Is(err, errPremiseUnchanged) {
		return s.View(ctx, userID, workspaceID)
	}
	if err != nil {
		return View{}, err
	}
	return s.viewOf(ctx, rec), nil
}

var (
	errPremiseUnchanged  = errors.New("folderagent: the premise already has this answer")
	errValidationPremise = errors.New("folderagent: answer confirmed or something_else")
)

// ErrPremiseAnswer reports an answer that is neither of the two.
func ErrPremiseAnswer(err error) bool { return errors.Is(err, errValidationPremise) }
