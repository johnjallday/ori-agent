package usagelimits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/authdiscovery"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/logger"
)

const claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

var errClaudeUsage = errors.New("claude usage endpoint unavailable")

// Only static explanations and numeric HTTP statuses may reach the snapshot.
// Never retain a transport error or provider response body here.
type claudeUsageFailure struct{ detail string }

func (e *claudeUsageFailure) Error() string { return e.detail }
func (e *claudeUsageFailure) Unwrap() error { return errClaudeUsage }

func claudeFailureDetail(err error) string {
	var failure *claudeUsageFailure
	if errors.As(err, &failure) {
		return failure.detail
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Claude usage request timed out; try Refresh again"
	}
	return "Claude usage endpoint unavailable"
}

var usageLog = newUsageLogger()

func newUsageLogger() *logger.Logger {
	log := logger.New("usage-limits")
	if os.Getenv("ORI_USAGE_LIMITS_DEBUG") == "1" {
		log.SetLevel(logger.DebugLevel)
	}
	return log
}

type claudeReader struct {
	lookPath      func(string) (string, error)
	discover      func(context.Context) (authdiscovery.ClaudeOauth, error)
	apiKeyPresent func() bool
	client        *http.Client
	endpoint      string
	now           func() time.Time
	live          *liveReader
	mirror        func() (Provider, error)
}

func claudeProvider() Provider {
	return Provider{Provider: "claude_code", DisplayName: "Claude Code", Level: LevelOK, Windows: []Window{}}
}

func newClaudeReader() *claudeReader {
	endpoint := claudeUsageURL
	if override := os.Getenv("ORI_USAGE_LIMITS_CLAUDE_USAGE_URL"); override != "" {
		endpoint = override
	}
	r := &claudeReader{
		lookPath: exec.LookPath, discover: authdiscovery.DiscoverClaudeOauthContext,
		apiKeyPresent: func() bool { return os.Getenv("ANTHROPIC_API_KEY") != "" },
		endpoint:      endpoint, now: time.Now,
		client: &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	r.live = &liveReader{now: time.Now, fetch: r.fetch}
	mirrorPath := filepath.Join(config.DefaultDataDir(), ClaudeMirrorFilename)
	r.mirror = func() (Provider, error) { return ReadClaudeMirror(mirrorPath, r.now()) }
	return r
}

func (r *claudeReader) read(ctx context.Context, _ bool) *Provider {
	if _, err := r.lookPath("claude"); err != nil {
		return nil
	}
	p, err := r.live.read(ctx)
	if err != nil {
		if r.mirror != nil {
			if fallback, mirrorErr := r.mirror(); mirrorErr == nil {
				fallback.Detail = "Claude usage endpoint unavailable; showing the status line reading from " + fallback.FetchedAt.Local().Format("15:04")
				return &fallback
			}
		}
		p = claudeProvider()
		p.State, p.Detail = StateUnavailable, claudeFailureDetail(err)
	}
	return &p
}

func (r *claudeReader) fetch(ctx context.Context) (Provider, error) {
	p := claudeProvider()
	oauth, err := r.discover(ctx)
	if errors.Is(err, authdiscovery.ErrNoClaudeLogin) || (err == nil && oauth.AccessToken == "") {
		if r.apiKeyPresent() {
			p.State, p.Detail = StateNoLimits, "Claude Code is using an API key, not a subscription"
		} else {
			p.State, p.Detail = StateNotSignedIn, "Open Claude Code and sign in"
		}
		return p, nil
	}
	if err != nil {
		return Provider{}, &claudeUsageFailure{"Ori could not read the Claude Code login in this environment"}
	}
	if strings.HasPrefix(oauth.AccessToken, "sk-ant-api") {
		p.State, p.Detail = StateNoLimits, "Claude Code is using an API key, not a subscription"
		return p, nil
	}
	if oauth.ExpiresAt != 0 && !time.UnixMilli(oauth.ExpiresAt).After(r.now()) {
		p.State, p.Detail = StateNotSignedIn, "Open Claude Code once to refresh its login"
		return p, nil
	}
	if !validClaudeUsageURL(r.endpoint) {
		return Provider{}, &claudeUsageFailure{"Claude usage endpoint configuration is invalid"}
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint, nil)
	if err != nil {
		return Provider{}, errClaudeUsage
	}
	req.Header.Set("Authorization", "Bearer "+oauth.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	response, err := r.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return Provider{}, &claudeUsageFailure{"Claude usage request timed out; try Refresh again"}
		}
		return Provider{}, &claudeUsageFailure{"Ori could not connect to the Claude usage endpoint"}
	}
	defer func() { _ = response.Body.Close() }()
	usageLog.Debug("Claude usage response", logger.Fields{"status": response.StatusCode})
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		p.State, p.Detail = StateNotSignedIn, "Open Claude Code once to refresh its login"
		return p, nil
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return Provider{}, &claudeUsageFailure{"Claude usage endpoint is rate-limiting requests (HTTP 429); try again later"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Provider{}, &claudeUsageFailure{fmt.Sprintf("Claude usage endpoint returned HTTP %d; try again later", response.StatusCode)}
	}
	const maxBody = 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return Provider{}, &claudeUsageFailure{"Ori could not read the Claude usage response"}
	}
	p, err = parseClaudeUsage(body, r.now())
	if err != nil {
		return Provider{}, &claudeUsageFailure{"Claude usage endpoint returned an unrecognized response"}
	}
	return p, nil
}

// A demo override may target loopback, not an arbitrary credential recipient.
// Redirects are disabled even for same-host redirects.
func validClaudeUsageURL(endpoint string) bool {
	if endpoint == claudeUsageURL {
		return true
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || u.Hostname() == "localhost"
}

func parseClaudeUsage(data []byte, fetchedAt time.Time) (Provider, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return Provider{}, errClaudeUsage
	}
	// Log only known schema keys. Unknown keys could themselves contain PII.
	known := []string{}
	fieldTypes := []string{}
	for _, key := range []string{"five_hour", "seven_day", "seven_day_sonnet", "seven_day_opus", "limits", "plan", "plan_type"} {
		if raw, ok := fields[key]; ok {
			known = append(known, key)
			fieldTypes = append(fieldTypes, key+":"+claudeJSONType(raw))
		}
	}
	sort.Strings(known)
	usageLog.Debug("Claude usage shape", logger.Fields{"keys": known, "types": fieldTypes})
	p := claudeProvider()
	p.State, p.Source, p.FetchedAt = StateOK, SourceClaudeOAuthUsage, fetchedAt.UTC()
	for _, key := range []string{"plan_type", "plan"} {
		if raw, ok := fields[key]; ok {
			var plan string
			if json.Unmarshal(raw, &plan) == nil {
				p.Plan = plan
				break
			}
		}
	}
	// Current responses may include both the canonical flat windows and an
	// additional limits field with a different schema. Its presence must not
	// override known windows or make an otherwise valid reading fail.
	flatKinds := []string{"five_hour", "seven_day", "seven_day_sonnet", "seven_day_opus"}
	hasFlat := false
	for _, kind := range flatKinds {
		if _, ok := fields[kind]; ok {
			hasFlat = true
		}
	}
	recognized := false
	if raw, ok := fields["limits"]; ok && !hasFlat {
		recognized = true
		var limits []struct {
			Kind     string     `json:"kind"`
			Percent  *float64   `json:"percent"`
			ResetsAt *time.Time `json:"resets_at"`
		}
		if json.Unmarshal(raw, &limits) != nil {
			return Provider{}, errClaudeUsage
		}
		for _, limit := range limits {
			w, ok := claudeWindow(limit.Kind)
			if !ok || limit.Percent == nil || *limit.Percent < 0 {
				return Provider{}, errClaudeUsage
			}
			w.UsedPercent = *limit.Percent
			if limit.ResetsAt != nil {
				w.ResetsAt = limit.ResetsAt.Unix()
			}
			p.Windows = append(p.Windows, w)
		}
	} else {
		for _, kind := range flatKinds {
			raw, ok := fields[kind]
			if !ok {
				continue
			}
			recognized = true
			var limit *struct {
				Percent  *float64   `json:"utilization"`
				ResetsAt *time.Time `json:"resets_at"`
			}
			if json.Unmarshal(raw, &limit) != nil {
				return Provider{}, errClaudeUsage
			}
			if limit == nil {
				continue
			}
			if limit.Percent == nil || *limit.Percent < 0 {
				return Provider{}, errClaudeUsage
			}
			w, _ := claudeWindow(kind)
			w.UsedPercent = *limit.Percent
			if limit.ResetsAt != nil {
				w.ResetsAt = limit.ResetsAt.Unix()
			}
			p.Windows = append(p.Windows, w)
		}
	}
	if !recognized {
		return Provider{}, errClaudeUsage
	}
	p.summarize(false)
	if len(p.Windows) == 0 {
		p.State, p.Detail = StateNoLimits, "This Claude Code account has no subscription usage windows"
	}
	return p, nil
}

// Diagnostics classify values without logging their contents or unknown keys.
func claudeJSONType(raw json.RawMessage) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "empty"
	}
	switch text[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case 'n':
		return "null"
	case '"':
		return "string"
	default:
		return "scalar"
	}
}

func claudeWindow(kind string) (Window, bool) {
	kind = strings.ToLower(kind)
	if kind == "five_hour" || kind == "5_hour" || kind == "5h" {
		return WindowFor(300), true
	}
	if kind == "seven_day" || kind == "weekly" || kind == "7_day" {
		return WindowFor(10080), true
	}
	for _, model := range []string{"sonnet", "opus"} {
		if strings.Contains(kind, model) {
			w := WindowFor(10080)
			w.ID, w.Label, w.ModelSpecific = "weekly_"+model, "Weekly ("+strings.ToUpper(model[:1])+model[1:]+")", true
			return w, true
		}
	}
	return Window{}, false
}
