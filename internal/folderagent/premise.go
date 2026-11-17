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
	// Names are the first songs' names while it is asked (at most
	// premiseNamesShown, in the order the folder was walked) and NamesMore is
	// how many more there are, so the owner can check the recognition against
	// their own songs. Plain text: they are folder names from disk, and the
	// card must draw them as text.
	Names     []string `json:"names,omitempty"`
	NamesMore int      `json:"names_more,omitempty"`
	// Outcome is the one sentence that says what the plan the card shows after
	// the confirm answer will make, written by the host from that plan (see
	// premiseOutcome); empty when it cannot be said truthfully. NothingYet says
	// that answering installs or creates nothing. Both are set only while it is
	// asked.
	Outcome    string `json:"outcome,omitempty"`
	NothingYet string `json:"nothing_yet,omitempty"`
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

// premiseView is the card's premise, nil when the record is not asked it. plan
// is the plan the view carries, which the outcome sentence is written from.
func premiseView(rec *Record, plan Plan) *PremiseView {
	if !premiseApplies(rec) {
		return nil
	}
	p := rec.Facts.Portfolio
	kind := premiseKind(p.HomeName)
	counts := joinAnd(foldermap.FormatCounts(rec.Facts))
	answer := currentPremise(rec)
	switch {
	case answer == nil:
		names, more := premiseNames(rec.Facts)
		return &PremiseView{State: PremiseAsked,
			Sentence: fmt.Sprintf("This looks like %s %s folder: %s.", article(kind), kind, counts),
			Names:    names, NamesMore: more,
			Outcome: premiseOutcome(rec, plan), NothingYet: premiseNothingYet,
			Confirm: PremiseConfirmLabel,
			Other:   "It's something else"}
	case answer.Answer == PremiseConfirmed:
		return &PremiseView{State: PremiseConfirmed,
			Sentence: fmt.Sprintf("%s %s folder: %s.", capitalize(article(kind)), kind, counts)}
	}
	return &PremiseView{State: PremiseSomethingElse, Sentence: "Nothing is installed or created.", Withdrawn: !answer.Relooked}
}

// premiseNothingYet is the line under the outcome: the answer shows the plan
// and changes nothing.
const premiseNothingYet = "Nothing is installed or created until you approve the plan."

// premiseOutcome is the one sentence that says what the plan, as the card
// shows it after "Yes, show me the plan", will make. It is written from the
// plan the view carries, so it cannot promise what that plan lacks: it names
// the Home only when the plan's chosen steps include the Home step, and the
// group only when they include the group step (never both: a chosen step
// excludes the other). Installs, filing and add-ons are not named, so no
// plugin appears in it, and the line under it already says nothing is
// installed yet. When neither step is chosen, or the plan cannot run, it is
// "" and the card shows only that line.
//
// Words come from the plan and the tables, never from this file: the Home's
// name from its step, the folder from the record, the card noun from the
// portfolio's shape, the group's noun from the same function the group step's
// title uses. The agents clause is added only when the plan hires an agent.
func premiseOutcome(rec *Record, plan Plan) string {
	if rec == nil || rec.Facts.Portfolio == nil || !plan.Runnable {
		return ""
	}
	var home, group *folderlook.Step
	for i := range plan.Steps {
		step := &plan.Steps[i]
		switch {
		case !step.Chosen:
		case step.Kind == folderlook.StepHome && home == nil:
			home = step
		case step.Kind == folderlook.StepGroup && group == nil:
			group = step
		}
	}
	folder := premiseName(rec.FolderName)
	if folder == "" {
		return ""
	}
	switch {
	case home != nil:
		name := strings.TrimSpace(home.Name)
		var candidate *folderlook.Candidate
		for i := range plan.Candidates {
			if plan.Candidates[i].ID == home.CandidateID {
				candidate = &plan.Candidates[i]
				break
			}
		}
		if name == "" || candidate == nil {
			return ""
		}
		noun := foldermap.ShapeNoun(rec.Facts.Portfolio.Shape, 1)
		agents := ""
		if len(plan.Hires) > 0 || plan.HiresNote != "" {
			agents = ", and agents to keep track of them"
		}
		if createsHome(*candidate) {
			return fmt.Sprintf("I'll plan %s %s for %s: a card for each %s%s.", article(name), name, folder, noun, agents)
		}
		return fmt.Sprintf("I'll plan adding %s to your %s: a card for each %s%s.", folder, name, noun, agents)
	case group != nil:
		return fmt.Sprintf("I'll plan turning %s into a group: a workspace for each %s.", folder, folderlook.MemberNoun(rec.Facts))
	}
	return ""
}

// PremiseConfirmLabel is the confirm answer's label. It says what pressing it
// does: the card then shows the plan. Nothing is installed or created by the
// press itself.
const PremiseConfirmLabel = "Yes, show me the plan"

const (
	// premiseNamesShown is how many song names the question shows.
	premiseNamesShown = 3
	// premiseNameMax is the longest name drawn, counting its ellipsis.
	premiseNameMax = 40
)

// premiseNames are the names of the first premiseNamesShown projects of the
// portfolio's shape, in the order the map was walked, and how many more there
// are. A project's name is the name of the folder that holds its project file,
// which is the name its card gets later, so a folder with several project
// files is one name. Control and format characters are dropped (a folder name
// comes from disk), a name longer than premiseNameMax is cut with "…", and a
// name with nothing left is skipped, not counted.
func premiseNames(facts foldermap.Facts) (names []string, more int) {
	if facts.Portfolio == nil {
		return nil, 0
	}
	for _, project := range facts.Members {
		if project.Shape != facts.Portfolio.Shape {
			continue
		}
		name := premiseName(project.Name)
		if name == "" {
			continue
		}
		if len(names) == premiseNamesShown {
			more++
			continue
		}
		names = append(names, name)
	}
	return names, more
}

// premiseName is one project's name as the question draws it.
func premiseName(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	runes := []rune(strings.TrimSpace(b.String()))
	if len(runes) > premiseNameMax {
		return strings.TrimSpace(string(runes[:premiseNameMax-1])) + "…"
	}
	return string(runes)
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
