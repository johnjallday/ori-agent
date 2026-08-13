package github

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// maxIssueFieldRunes bounds one issue field (title, body, a comment body) so a
// pathological remote payload cannot make the durable snapshot unbounded. It
// is generous on purpose: a real Issue body or comment is prose, not a
// terminal cell, and truncating it silently would make the snapshot lie about
// the requirement it was meant to preserve.
const maxIssueFieldRunes = 200_000

// maxIssueComments bounds how many comments one snapshot embeds, for the same
// reason.
const maxIssueComments = 500

// Issue is the exact snapshot of one GitHub Issue returned by a single fresh
// `gh issue view --json` read. Every text field has already been through
// SanitizeText: control and terminal-escape bytes are removed, but
// nothing else is altered — Markdown fences, quotes, backticks, command
// substitutions, and leading dashes all survive as inert text, because this
// content is written into a snapshot file and never evaluated.
type Issue struct {
	Number int
	Title  string
	Body   string
	URL    string
	// State is the normalized remote state: "open" or "closed".
	State     string
	Labels    []string
	Comments  []IssueComment
	FetchedAt time.Time
}

// IssueComment is one comment on the Issue, in the order GitHub returned it.
type IssueComment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

var issueJSONFields = strings.Join([]string{
	"number", "title", "body", "url", "state", "labels", "comments",
}, ",")

// GetIssue performs one fresh, read-only `gh issue view` query for number. It
// never labels, comments on, closes, or otherwise mutates the Issue.
func (c *Client) GetIssue(ctx context.Context, number int) (Issue, error) {
	if number <= 0 {
		return Issue{}, errors.New("issue number must be a positive integer")
	}
	queryCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	output, err := c.run(queryCtx, "issue", "view", strconv.Itoa(number), "--json", issueJSONFields)
	if err != nil {
		return Issue{}, classify(queryCtx, err)
	}
	return decodeIssue(output)
}

type rawIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Comments []struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		Body      string `json:"body"`
		CreatedAt string `json:"createdAt"`
	} `json:"comments"`
}

func decodeIssue(output []byte) (Issue, error) {
	if len(output) > MaxOutputBytes {
		return Issue{}, &Error{Kind: ErrorMalformed, Detail: "the GitHub response was larger than this tool will decode"}
	}
	var raw rawIssue
	if err := json.Unmarshal(output, &raw); err != nil {
		return Issue{}, &Error{Kind: ErrorMalformed, Detail: "the GitHub response could not be decoded"}
	}
	if raw.Number <= 0 {
		return Issue{}, &Error{Kind: ErrorMalformed, Detail: "the GitHub response named no Issue number"}
	}
	issue := Issue{
		Number:    raw.Number,
		Title:     SanitizeText(raw.Title),
		Body:      SanitizeText(raw.Body),
		URL:       sanitize(raw.URL),
		State:     strings.ToLower(strings.TrimSpace(raw.State)),
		FetchedAt: time.Now(),
	}
	for _, label := range raw.Labels {
		name := sanitize(label.Name)
		if name != "" {
			issue.Labels = append(issue.Labels, name)
		}
	}
	comments := raw.Comments
	if len(comments) > maxIssueComments {
		comments = comments[:maxIssueComments]
	}
	for _, comment := range comments {
		entry := IssueComment{
			Author: sanitize(comment.Author.Login),
			Body:   SanitizeText(comment.Body),
		}
		if parsed, err := time.Parse(time.RFC3339, comment.CreatedAt); err == nil {
			entry.CreatedAt = parsed
		}
		issue.Comments = append(issue.Comments, entry)
	}
	return issue, nil
}

// SanitizeText prepares one long-form Issue field (title, body, a
// comment) for a durable snapshot file. Unlike sanitize/boundedText, which
// bound short fields destined for a terminal cell, this preserves newlines
// and tabs — the snapshot is Markdown prose, not a single line — and does not
// ellipsize short content. It strips only bytes that could hijack a terminal
// or reorder text invisibly: C0/C1 controls other than newline and tab, and
// bidirectional-override / line-separator code points. Backticks, fenced code
// blocks, quotes, command substitutions, and leading dashes all survive
// untouched, because this text is written to a file and never evaluated.
func SanitizeText(value string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 32, r == 127:
			return -1
		case r >= 0x80 && r <= 0x9f:
			return -1
		case r == 0x200e, r == 0x200f:
			return -1
		case r >= 0x202a && r <= 0x202e:
			return -1
		case r >= 0x2066 && r <= 0x2069:
			return -1
		case r == 0x2028, r == 0x2029:
			return -1
		default:
			return r
		}
	}, value)
	// Trim only surrounding whitespace/newlines, never interior content.
	cleaned = strings.Trim(cleaned, "\n\r\t ")
	runes := []rune(cleaned)
	if len(runes) > maxIssueFieldRunes {
		return string(runes[:maxIssueFieldRunes]) + "\n\n…(truncated)"
	}
	return cleaned
}
