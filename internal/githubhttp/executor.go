package githubhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/mcp"
)

// apiExecutor is the only code in Ori that writes to GitHub.
//
// It is reached exclusively from Broker.Confirm, which runs only after a user
// has approved the exact content. Keeping it a single small type with one
// entry point is deliberate: "there is no second route to a GitHub write" is a
// claim that has to be checkable by reading, and a reader can check it here.
type apiExecutor struct {
	conn *Connection
}

// NewExecutor builds the executor over the global connection.
func NewExecutor(conn *Connection) Executor { return &apiExecutor{conn: conn} }

// Apply performs one confirmed change and returns the resulting GitHub URL.
func (e *apiExecutor) Apply(ctx context.Context, change Change) (string, error) {
	if e == nil || e.conn == nil {
		return "", fmt.Errorf("github: the connection is unavailable")
	}
	owner, repo, ok := SplitRepo(change.Repo)
	if !ok {
		return "", fmt.Errorf("github: %q is not a repository reference", change.Repo)
	}

	token, stored, err := mcp.LoadStaticBearerToken(ctx, MCPServerConfig())
	if err != nil || !stored {
		return "", fmt.Errorf("github: this workspace's GitHub connection is not available")
	}

	switch change.Kind {
	case ProposalComment:
		return e.comment(ctx, token, owner, repo, change)
	case ProposalLabels:
		return e.labels(ctx, token, owner, repo, change)
	case ProposalState:
		return e.state(ctx, token, owner, repo, change)
	default:
		return "", fmt.Errorf("github: %q is not a change this workspace can apply", change.Kind)
	}
}

func (e *apiExecutor) comment(ctx context.Context, token, owner, repo string, change Change) (string, error) {
	body, err := json.Marshal(map[string]string{"body": change.Body})
	if err != nil {
		return "", fmt.Errorf("github: could not prepare the comment")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments",
		apiBaseURL, url.PathEscape(owner), url.PathEscape(repo), change.Issue)

	resp, err := e.conn.doWithBody(ctx, token, http.MethodPost, endpoint, string(body))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		return "", applyError(resp, "post the comment")
	}

	var created struct {
		HTMLURL string `json:"html_url"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)
	return created.HTMLURL, nil
}

// labels applies removals before additions, so a proposal that swaps one label
// for another cannot leave the issue briefly carrying neither.
func (e *apiExecutor) labels(ctx context.Context, token, owner, repo string, change Change) (string, error) {
	base := fmt.Sprintf("%s/repos/%s/%s/issues/%d",
		apiBaseURL, url.PathEscape(owner), url.PathEscape(repo), change.Issue)

	for _, label := range change.RemoveLabels {
		endpoint := base + "/labels/" + url.PathEscape(label)
		resp, err := e.conn.do(ctx, token, http.MethodDelete, endpoint)
		if err != nil {
			return "", err
		}
		status := resp.StatusCode
		_ = resp.Body.Close()
		// A label that is already absent is the state the user asked for,
		// so it is not a failure.
		if status != http.StatusOK && status != http.StatusNotFound {
			return "", fmt.Errorf("github: could not remove the label %q", label)
		}
	}

	if len(change.AddLabels) > 0 {
		body, err := json.Marshal(map[string][]string{"labels": change.AddLabels})
		if err != nil {
			return "", fmt.Errorf("github: could not prepare the labels")
		}
		resp, err := e.conn.doWithBody(ctx, token, http.MethodPost, base+"/labels", string(body))
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", applyError(resp, "apply the labels")
		}
	}

	return issueURL(owner, repo, change.Issue), nil
}

func (e *apiExecutor) state(ctx context.Context, token, owner, repo string, change Change) (string, error) {
	payload := map[string]string{"state": change.State}
	if change.State == "closed" && strings.TrimSpace(change.StateReason) != "" {
		payload["state_reason"] = change.StateReason
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("github: could not prepare the change")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues/%d",
		apiBaseURL, url.PathEscape(owner), url.PathEscape(repo), change.Issue)

	resp, err := e.conn.doWithBody(ctx, token, http.MethodPatch, endpoint, string(body))
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", applyError(resp, "change the issue state")
	}
	return issueURL(owner, repo, change.Issue), nil
}

func issueURL(owner, repo string, issue int) string {
	return fmt.Sprintf("https://github.com/%s/%s/issues/%d", owner, repo, issue)
}

// applyError turns a failed write into plain language. GitHub's own error body
// is deliberately not included: it is written for API consumers, and this text
// is shown to someone who just clicked Approve.
func applyError(resp *http.Response, action string) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("github: the connection was rejected, so Ori could not %s. Reconnect GitHub in Settings", action)
	case http.StatusForbidden, http.StatusNotFound:
		if isRateLimited(resp) {
			return fmt.Errorf("github: %s", strings.ToLower(rateLimitMessage(resp)))
		}
		return fmt.Errorf("github: this token is not permitted to %s on that repository", action)
	case http.StatusGone:
		return fmt.Errorf("github: issues are disabled on that repository, so Ori could not %s", action)
	default:
		return fmt.Errorf("github: could not %s right now. Nothing was changed; try again in a moment", action)
	}
}
