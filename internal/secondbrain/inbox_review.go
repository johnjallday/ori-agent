package secondbrain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/sensitive"
)

// Inbox review is intentionally narrower than a look. Only short standalone
// imperatives in plain prose qualify. Never infer an assignee, workspace or
// calendar date. Checklist and Logseq notes belong to the existing look path.
var imperative = regexp.MustCompile(`^(Compare|Review|Send|Ask|Call|Check|Schedule|Book) [\pL\pN][\pL\pN ,’'/-]*$`)
var uncertain = regexp.MustCompile(`(?i)\b(not|never|don't|didn't|already|instead|cancelled|canceled|said|asked|yesterday|if|unless|was|were|maybe|might|could|should)\b`)

func reviewHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func ReviewNoteKey(ref, rel string) string { return reviewHash(ref, rel) }
func ReviewRevision(body string) string    { return reviewHash(body) }

type InboxAction struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Excerpt   string `json:"excerpt"`
	Revision  string `json:"revision"`
	State     string `json:"state"`
	Workspace string `json:"workspace_id,omitempty"`
	Ticket    string `json:"ticket_id,omitempty"`
}

func InboxPassages(key, body string) []InboxAction {
	// The reader shows at most PreviewBodyBytes. Don't suggest unseen prose.
	if len(body) > PreviewBodyBytes || strings.ContainsAny(body, "\"“”`<>[]?:;!") || strings.Contains(body, "~~~") || sensitive.ContainsSecretLikeText(body) {
		return nil
	}
	prose, _, ok := strings.Cut(body, "\n\nCaptured by Ori on ")
	if !ok {
		return nil
	}
	// Never split reported speech or a completed historical account into a new imperative.
	var out []InboxAction
	seen := map[string]bool{}
	for _, line := range strings.Split(prose, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "-") || strings.HasPrefix(strings.TrimSpace(line), "TODO ") || strings.Contains(line, "‘") {
			continue
		}
		for _, sentence := range strings.SplitAfter(line, ". ") {
			excerpt := strings.TrimSpace(sentence)
			title := strings.TrimSuffix(excerpt, ".")
			if !imperative.MatchString(title) || uncertain.MatchString(title) || utf8.RuneCountInString(title) > 160 {
				continue
			}
			id := reviewHash(key, strings.ToLower(strings.Join(strings.Fields(title), " ")))
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, InboxAction{ID: id, Title: title, Excerpt: excerpt, Revision: ReviewRevision(body), State: "pending"})
			if len(out) == 4 {
				return out
			}
		}
	}
	return out
}

// ReviewLedger stores only opaque identities and decisions, never note text.
// Missing rows are not evidence that an old note is eligible.
type ReviewLedger struct{ DB *sql.DB }

func (l ReviewLedger) Register(ctx context.Context, key string, actions []InboxAction) error {
	for _, a := range actions {
		if _, err := l.DB.ExecContext(ctx, `INSERT INTO inbox_review_actions(note_key,action_id,revision) VALUES(?,?,?) ON CONFLICT DO NOTHING`, key, a.ID, a.Revision); err != nil {
			return err
		}
	}
	return nil
}

func (l ReviewLedger) Read(ctx context.Context, key, rev string) (map[string]InboxAction, error) {
	rows, err := l.DB.QueryContext(ctx, `SELECT action_id,state,workspace_id,ticket_id FROM inbox_review_actions WHERE note_key=? AND revision=?`, key, rev)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]InboxAction{}
	for rows.Next() {
		var a InboxAction
		if err := rows.Scan(&a.ID, &a.State, &a.Workspace, &a.Ticket); err != nil {
			return nil, err
		}
		out[a.ID] = a
	}
	return out, rows.Err()
}
