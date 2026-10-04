package emailtriage

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/mailbox"
)

const owner = "me@example.com"

func thread(messages ...mailbox.Message) mailbox.Thread {
	return mailbox.Thread{ID: "t", Messages: messages}
}

func from(address string) mailbox.Participant { return mailbox.Participant{Address: address} }

func TestClassify(t *testing.T) {
	toMe := []mailbox.Participant{from(owner)}
	cases := []struct {
		name     string
		thread   mailbox.Thread
		bucket   Bucket
		rule     string
		askModel bool
	}{
		{"empty", thread(), BucketHandled, RuleEmpty, false},
		{"you wrote last", thread(mailbox.Message{From: from("sam@example.com")}, mailbox.Message{From: from(owner), FromUser: true}), BucketHandled, RuleYouWroteLast, false},
		{"you replied from another client", thread(mailbox.Message{From: from("sam@example.com"), Answered: true}), BucketHandled, RuleYouReplied, false},
		{"newsletter", thread(mailbox.Message{From: from("news@shop.example"), Bulk: true, Unread: true}), BucketIgnorable, RuleBulk, false},
		{"auto-submitted alert", thread(mailbox.Message{From: from("alerts@bank.example"), AutoSubmitted: true}), BucketFYI, RuleAutomated, true},
		{"no-reply sender", thread(mailbox.Message{From: from("no-reply@accounts.google.com"), To: toMe}), BucketFYI, RuleAutomated, true},
		{"no-reply with a tag", thread(mailbox.Message{From: from("noreply+billing@shop.example")}), BucketFYI, RuleAutomated, true},
		{"copied only", thread(mailbox.Message{From: from("sam@example.com"), To: []mailbox.Participant{from("team@example.com")}, Cc: toMe}), BucketFYI, RuleCopiedOnly, true},
		{"a person to you", thread(mailbox.Message{From: from("sam@example.com"), To: toMe, Unread: true}), BucketNeedsYou, RuleAddressedToYou, true},
		// Opening a question does not answer it.
		{"a person to you, already read", thread(mailbox.Message{From: from("sam@example.com"), To: toMe}), BucketNeedsYou, RuleAddressedToYou, true},
		{"no visible To counts as to you", thread(mailbox.Message{From: from("sam@example.com"), Cc: toMe}), BucketNeedsYou, RuleAddressedToYou, true},
		// A name that merely contains "alert" is a person.
		{"a person named alertson", thread(mailbox.Message{From: from("alertson@example.com"), To: toMe}), BucketNeedsYou, RuleAddressedToYou, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.thread, owner)
			if got.Bucket != tc.bucket || got.Rule != tc.rule || got.AskModel != tc.askModel {
				t.Fatalf("Classify = %+v, want bucket %s rule %s askModel %v", got, tc.bucket, tc.rule, tc.askModel)
			}
		})
	}
}
