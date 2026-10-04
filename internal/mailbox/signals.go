package mailbox

import "strings"

// signalHeaders are the headers that say who sent a message: a mailing list
// (List-Unsubscribe, List-Id, Precedence) or a machine (Auto-Submitted). Both
// readers ask the server for exactly these.
var signalHeaders = []string{"List-Unsubscribe", "List-Id", "Precedence", "Auto-Submitted"}

// senderSignals reads Bulk and AutoSubmitted from a message's headers. get
// returns a header's value, or "" when it is absent.
func senderSignals(get func(name string) string) (bulk, autoSubmitted bool) {
	if strings.TrimSpace(get("List-Unsubscribe")) != "" || strings.TrimSpace(get("List-Id")) != "" {
		bulk = true
	}
	switch strings.ToLower(strings.TrimSpace(get("Precedence"))) {
	case "bulk", "list", "junk":
		bulk = true
	}
	// RFC 3834: "no" is the one value a person's own message may carry.
	if value := strings.ToLower(strings.TrimSpace(get("Auto-Submitted"))); value != "" && value != "no" {
		autoSubmitted = true
	}
	return bulk, autoSubmitted
}
