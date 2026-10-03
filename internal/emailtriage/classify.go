// Package emailtriage decides which inbox threads need the user. Rules sort
// every thread first, from facts the mail server states (who sent it, whether
// it was sent to a list, whether the user already replied); a model then looks
// only at the few threads the rules cannot settle, to say why each one matters.
package emailtriage

import (
	"strings"

	"github.com/johnjallday/ori-agent/internal/mailbox"
)

// Bucket is where a thread lands.
type Bucket string

const (
	// BucketNeedsYou is a thread the user should act on.
	BucketNeedsYou Bucket = "needs_you"
	// BucketFYI is worth knowing about but asks nothing of the user.
	BucketFYI Bucket = "fyi"
	// BucketIgnorable is mail sent to a list: newsletters, promotions.
	BucketIgnorable Bucket = "ignorable"
	// BucketHandled is a thread the user already answered; it is not listed.
	BucketHandled Bucket = "handled"
)

// Kind says what a thread needs from the user.
type Kind string

const (
	KindReply    Kind = "reply"
	KindDeadline Kind = "deadline"
	KindDecision Kind = "decision"
	KindInfo     Kind = "info"
)

// Rule codes are stable, so a verdict can be explained and tested.
const (
	RuleEmpty           = "empty"
	RuleYouWroteLast    = "you_wrote_last"
	RuleYouReplied      = "you_replied"
	RuleBulk            = "bulk"
	RuleAutomated       = "automated_sender"
	RuleCopiedOnly      = "copied_only"
	RuleAddressedToYou  = "addressed_to_you"
	RuleModel           = "model"
	RuleDismissedByUser = "dismissed"
)

// Verdict is the rules' call on one thread.
type Verdict struct {
	Bucket Bucket
	Kind   Kind
	Rule   string
	// AskModel marks a thread the rules cannot settle: a person writing to the
	// user, or a machine message that may carry a deadline.
	AskModel bool
}

// automatedLocalParts are sender names that mean a machine wrote the message.
var automatedLocalParts = []string{
	"noreply", "no-reply", "no_reply", "donotreply", "do-not-reply", "do_not_reply",
	"mailer-daemon", "postmaster", "bounce", "bounces", "notification", "notifications",
	"alert", "alerts", "automated", "auto-confirm",
}

// Classify sorts one thread. owner is the mailbox's own address.
func Classify(thread mailbox.Thread, owner string) Verdict {
	if len(thread.Messages) == 0 {
		return Verdict{Bucket: BucketHandled, Rule: RuleEmpty}
	}
	last := thread.Messages[len(thread.Messages)-1]
	switch {
	case last.FromUser:
		return Verdict{Bucket: BucketHandled, Rule: RuleYouWroteLast}
	case last.Answered:
		return Verdict{Bucket: BucketHandled, Rule: RuleYouReplied}
	case last.Bulk:
		return Verdict{Bucket: BucketIgnorable, Kind: KindInfo, Rule: RuleBulk}
	case last.AutoSubmitted || automatedSender(last.From.Address):
		// Receipts and alerts rarely need the user, but a bill or an expiring
		// login sometimes does; the model can promote it.
		return Verdict{Bucket: BucketFYI, Kind: KindInfo, Rule: RuleAutomated, AskModel: true}
	case copiedOnly(last, owner):
		return Verdict{Bucket: BucketFYI, Kind: KindInfo, Rule: RuleCopiedOnly, AskModel: true}
	}
	// A person wrote to the user and nobody has answered. Having opened it does
	// not change that.
	return Verdict{Bucket: BucketNeedsYou, Kind: KindReply, Rule: RuleAddressedToYou, AskModel: true}
}

func automatedSender(address string) bool {
	at := strings.LastIndex(address, "@")
	if at <= 0 {
		return false
	}
	local := strings.ToLower(address[:at])
	for _, name := range automatedLocalParts {
		if local == name || strings.HasPrefix(local, name+"+") || strings.HasPrefix(local, name+".") {
			return true
		}
	}
	return false
}

// copiedOnly is true when the user is on Cc and the message was addressed to
// someone else. A message with no visible To counts as addressed to the user.
func copiedOnly(message mailbox.Message, owner string) bool {
	owner = strings.TrimSpace(owner)
	if owner == "" || len(message.To) == 0 {
		return false
	}
	for _, p := range message.To {
		if strings.EqualFold(p.Address, owner) {
			return false
		}
	}
	for _, p := range message.Cc {
		if strings.EqualFold(p.Address, owner) {
			return true
		}
	}
	return false
}
