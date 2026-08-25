// Package config loads the checked-in, opt-in devflow configuration.
package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

const DefaultConfigRelativePath = ".herdr/devflow.toml"

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var sourcePattern = regexp.MustCompile(`^[A-Za-z0-9:._-]{1,80}$`)
var supportedAgentKinds = map[string]struct{}{
	"pi": {}, "claude": {}, "codex": {}, "gemini": {}, "cursor": {}, "devin": {}, "agy": {}, "cline": {}, "omp": {}, "mastracode": {}, "opencode": {}, "copilot": {}, "kimi": {}, "kiro": {}, "droid": {}, "amp": {}, "grok": {}, "hermes": {}, "kilo": {}, "qodercli": {}, "maki": {},
}

type Config struct {
	Bridge    BridgeConfig    `toml:"bridge"`
	Primary   PrimaryConfig   `toml:"primary"`
	Roles     RolesConfig     `toml:"roles"`
	Bootstrap BootstrapConfig `toml:"bootstrap"`
	Scheduler SchedulerConfig `toml:"scheduler"`
	Metadata  MetadataConfig  `toml:"metadata"`
	Status    StatusConfig    `toml:"status"`
	Overnight OvernightConfig `toml:"overnight"`
}

// OvernightConfig holds the defaults an Overnight Run offers before the user
// confirms one. Nothing here starts a run or changes an existing schedule; a
// run is created only from an explicit, confirmed command, and these values are
// the starting point that command presents.
type OvernightConfig struct {
	// StartTime is the default local start, as HH:MM. "now" means immediately.
	StartTime string `toml:"start_time"`
	// Deadline is the default absolute morning boundary, as HH:MM. It may be
	// earlier in the day than StartTime, which is how a run crosses midnight.
	Deadline string `toml:"deadline"`
	// Timezone is the IANA zone both times are interpreted in. Empty means the
	// machine's local zone, resolved when a run is created.
	Timezone string `toml:"timezone"`
	// MaxResumes is the default ceiling on acknowledged post-reset
	// continuations.
	MaxResumes int `toml:"max_resumes"`
	// WakeLead is how far before a reset the Mac may be woken so macOS,
	// network, Ori, and Herdr are ready. It never permits an early prompt.
	WakeLead string `toml:"wake_lead"`
}

type BridgeConfig struct {
	SchemaVersion   int    `toml:"schema_version"`
	Enabled         bool   `toml:"enabled"`
	MinHerdrVersion string `toml:"min_herdr_version"`
	SourceID        string `toml:"source_id"`
}

type PrimaryConfig struct {
	Role  string `toml:"role"`
	Kind  string `toml:"kind"`
	Model string `toml:"model"`
}

type RolesConfig struct {
	DefaultKind  string            `toml:"default_kind"`
	DefaultModel string            `toml:"default_model"`
	Defaults     map[string]string `toml:"defaults"`
	Models       map[string]string `toml:"models"`
}

// AgentSelection is the configured or explicitly requested kind/model pair for
// one agent launch. An empty Model deliberately delegates model selection to
// the external integration.
type AgentSelection struct {
	Kind  string `json:"kind"`
	Model string `json:"model"`
}

type BootstrapConfig struct {
	Template       string `toml:"template"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
}

type SchedulerConfig struct {
	RetryWindow string `toml:"retry_window"`
}

type MetadataConfig struct {
	Enabled bool `toml:"enabled"`
}

type StatusConfig struct {
	WatchPollInterval string `toml:"watch_poll_interval"`
	// GitHubTimeout bounds one `gh` invocation.
	GitHubTimeout string `toml:"github_timeout"`
	// GitHubRefreshInterval is the minimum gap between remote queries while
	// watching. Local and Herdr polling stays fast; only the network call is
	// rate limited, so a board left open cannot storm the GitHub API.
	GitHubRefreshInterval string `toml:"github_refresh_interval"`
	// GitHubCandidateLimit bounds how many pull requests are requested.
	GitHubCandidateLimit int `toml:"github_candidate_limit"`
}

func Default() Config {
	return Config{
		Bridge: BridgeConfig{
			SchemaVersion:   1,
			Enabled:         true,
			MinHerdrVersion: "0.7.5",
			SourceID:        "ori.devflow",
		},
		Primary: PrimaryConfig{Role: "builder", Kind: "claude", Model: ""},
		Roles: RolesConfig{
			DefaultKind:  "claude",
			DefaultModel: "",
			Defaults: map[string]string{
				"reviewer": "claude",
				"tester":   "claude",
			},
			Models: map[string]string{},
		},
		Bootstrap: BootstrapConfig{Template: "primary-v1", TimeoutSeconds: 30},
		Scheduler: SchedulerConfig{RetryWindow: "15m"},
		Metadata:  MetadataConfig{Enabled: true},
		Status: StatusConfig{
			WatchPollInterval:     "2s",
			GitHubTimeout:         "20s",
			GitHubRefreshInterval: "60s",
			GitHubCandidateLimit:  100,
		},
		Overnight: OvernightConfig{
			StartTime:  "now",
			Deadline:   "07:00",
			MaxResumes: DefaultMaxResumes,
			WakeLead:   "2m",
		},
	}
}

const (
	// DefaultMaxResumes is the PRD's default ceiling on acknowledged post-reset
	// continuations.
	DefaultMaxResumes = 3
	// MaxAllowedResumes bounds what the ceiling may be configured to. A night
	// holds at most a handful of five-hour windows, so a larger number cannot
	// describe a real night — it can only describe a loop nobody is watching.
	MaxAllowedResumes = 6
	// MinWakeLead and MaxWakeLead bound how early the Mac may wake before a
	// reset. Too short and macOS is not ready; too long and the machine is
	// awake for no reason.
	MinWakeLead = 30 * time.Second
	MaxWakeLead = 15 * time.Minute
)

// Load reads a config file, rejects unknown keys, applies explicit environment
// overrides, and validates the resulting effective configuration.
func Load(path string, lookupEnv func(string) (string, bool)) (Config, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	cfg := Default()
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("read devflow config: %w", err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		return Config{}, fmt.Errorf("unknown devflow config key(s): %s", strings.Join(keys, ", "))
	}
	if err := applyOverrides(&cfg, lookupEnv); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.Bridge.SchemaVersion != 1 {
		return fmt.Errorf("bridge.schema_version must be 1")
	}
	if !sourcePattern.MatchString(c.Bridge.SourceID) {
		return fmt.Errorf("bridge.source_id must use 1-80 ASCII letters, digits, colon, dot, underscore, or hyphen")
	}
	if _, err := ParseVersion(c.Bridge.MinHerdrVersion); err != nil {
		return fmt.Errorf("bridge.min_herdr_version: %w", err)
	}
	if !identifierPattern.MatchString(c.Primary.Role) {
		return fmt.Errorf("primary.role must match %s", identifierPattern.String())
	}
	if err := ValidateAgentSelection(c.Primary.Kind, c.Primary.Model); err != nil {
		return fmt.Errorf("primary: %w", err)
	}
	if err := ValidateAgentSelection(c.Roles.DefaultKind, c.Roles.DefaultModel); err != nil {
		return fmt.Errorf("roles: %w", err)
	}
	for role, kind := range c.Roles.Defaults {
		if !identifierPattern.MatchString(role) {
			return fmt.Errorf("roles.defaults.%s must match %s", role, identifierPattern.String())
		}
		if !supportedAgentKind(kind) {
			return fmt.Errorf("roles.defaults.%s must be a Herdr-supported agent kind", role)
		}
	}
	for role, agentModel := range c.Roles.Models {
		if !identifierPattern.MatchString(role) {
			return fmt.Errorf("roles.models.%s must match %s", role, identifierPattern.String())
		}
		if err := ValidateAgentModel(agentModel); err != nil {
			return fmt.Errorf("roles.models.%s: %w", role, err)
		}
	}
	if c.Bootstrap.Template != "primary-v1" {
		return fmt.Errorf("bootstrap.template must be primary-v1")
	}
	if c.Bootstrap.TimeoutSeconds < 3 || c.Bootstrap.TimeoutSeconds > 300 {
		return fmt.Errorf("bootstrap.timeout_seconds must be between 3 and 300")
	}
	if retry, err := time.ParseDuration(c.Scheduler.RetryWindow); err != nil || retry <= 0 {
		return fmt.Errorf("scheduler.retry_window must be a positive Go duration")
	}
	if interval, err := time.ParseDuration(c.Status.WatchPollInterval); err != nil || interval <= 0 {
		return fmt.Errorf("status.watch_poll_interval must be a positive Go duration")
	}
	if timeout, err := time.ParseDuration(c.Status.GitHubTimeout); err != nil || timeout < time.Second || timeout > 2*time.Minute {
		return fmt.Errorf("status.github_timeout must be a Go duration between 1s and 2m")
	}
	if err := c.Overnight.validate(); err != nil {
		return err
	}
	// A floor of 30s is deliberate. The remote clock exists to keep a watched
	// board from hammering the API; letting it be configured to 1s would
	// defeat the only protection the tool has.
	if interval, err := time.ParseDuration(c.Status.GitHubRefreshInterval); err != nil || interval < MinGitHubRefreshInterval {
		return fmt.Errorf("status.github_refresh_interval must be a Go duration of at least %s", MinGitHubRefreshInterval)
	}
	if c.Status.GitHubCandidateLimit < 1 || c.Status.GitHubCandidateLimit > 500 {
		return fmt.Errorf("status.github_candidate_limit must be between 1 and 500")
	}
	return nil
}

func supportedAgentKind(kind string) bool {
	if !identifierPattern.MatchString(kind) {
		return false
	}
	_, ok := supportedAgentKinds[kind]
	return ok
}

// IsSupportedAgentKind exposes the current Herdr 0.7.5 kind allow-list to
// command handlers that accept an explicit `--kind` argument.
func IsSupportedAgentKind(kind string) bool {
	return supportedAgentKind(kind)
}

// MaxAgentModelLength bounds the opaque model argument retained in config and
// state. Model names are not interpreted or provider-allow-listed here.
const MaxAgentModelLength = 256

// ValidateAgentModel accepts an empty integration default or one bounded,
// opaque argv value. Flag-shaped and control-bearing values are rejected before
// they can blur the Herdr command boundary or forge terminal output.
func ValidateAgentModel(agentModel string) error {
	if agentModel == "" {
		return nil
	}
	if !utf8.ValidString(agentModel) {
		return fmt.Errorf("model must be valid UTF-8")
	}
	if len(agentModel) > MaxAgentModelLength {
		return fmt.Errorf("model must be at most %d bytes", MaxAgentModelLength)
	}
	if strings.HasPrefix(agentModel, "-") {
		return fmt.Errorf("model must not be flag-shaped")
	}
	for _, character := range agentModel {
		if unicode.IsControl(character) {
			return fmt.Errorf("model must not contain control characters")
		}
	}
	return nil
}

// ValidateAgentSelection validates a complete kind/model launch pair without
// imposing any provider-specific meaning on the model value.
func ValidateAgentSelection(kind, agentModel string) error {
	if !supportedAgentKind(kind) {
		return fmt.Errorf("kind must be a Herdr-supported agent kind")
	}
	if err := ValidateAgentModel(agentModel); err != nil {
		return err
	}
	return nil
}

// RoleAgentSelection returns the configured pair for role. Primary, per-role,
// and fallback defaults use the same lookup rules.
func (c Config) RoleAgentSelection(role string) AgentSelection {
	if role == c.Primary.Role {
		return AgentSelection{Kind: c.Primary.Kind, Model: c.Primary.Model}
	}
	selection := AgentSelection{Kind: c.Roles.DefaultKind, Model: c.Roles.DefaultModel}
	kind, hasKind := c.Roles.Defaults[role]
	agentModel, hasModel := c.Roles.Models[role]
	if hasKind {
		if kind != selection.Kind && !hasModel {
			selection.Model = ""
		}
		selection.Kind = kind
	}
	if hasModel {
		selection.Model = agentModel
	}
	return selection
}

// ResolveAgentSelection overlays optional invocation values on a role's
// configured pair. A model-only override keeps the configured kind. A changed
// explicit kind with no explicit model clears the configured model so a stale
// model chosen for another integration is never inherited.
func (c Config) ResolveAgentSelection(role, kind, agentModel string) (AgentSelection, error) {
	selection := c.RoleAgentSelection(role)
	if kind != "" {
		if kind != selection.Kind && agentModel == "" {
			selection.Model = ""
		}
		selection.Kind = kind
	}
	if agentModel != "" {
		selection.Model = agentModel
	}
	if err := ValidateAgentSelection(selection.Kind, selection.Model); err != nil {
		return AgentSelection{}, err
	}
	return selection, nil
}

func (c Config) RoleKind(role string) string {
	return c.RoleAgentSelection(role).Kind
}

func (c Config) RetryWindow() time.Duration {
	duration, _ := time.ParseDuration(c.Scheduler.RetryWindow)
	return duration
}

func (c Config) WatchPollInterval() time.Duration {
	duration, _ := time.ParseDuration(c.Status.WatchPollInterval)
	return duration
}

// MinGitHubRefreshInterval is the shortest gap allowed between remote queries
// while watching.
const MinGitHubRefreshInterval = 30 * time.Second

// GitHubTimeout is the effective bound on one `gh` invocation.
func (c Config) GitHubTimeout() time.Duration {
	duration, _ := time.ParseDuration(c.Status.GitHubTimeout)
	return duration
}

// GitHubRefreshInterval is the effective minimum gap between remote queries.
func (c Config) GitHubRefreshInterval() time.Duration {
	duration, _ := time.ParseDuration(c.Status.GitHubRefreshInterval)
	if duration < MinGitHubRefreshInterval {
		return MinGitHubRefreshInterval
	}
	return duration
}

func applyOverrides(cfg *Config, lookupEnv func(string) (string, bool)) error {
	if value, ok := lookupEnv("HERDR_DEVFLOW_ENABLED"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("HERDR_DEVFLOW_ENABLED must be true or false")
		}
		cfg.Bridge.Enabled = parsed
	}
	if value, ok := lookupEnv("HERDR_DEVFLOW_MIN_HERDR_VERSION"); ok {
		cfg.Bridge.MinHerdrVersion = value
	}
	if value, ok := lookupEnv("HERDR_DEVFLOW_SOURCE_ID"); ok {
		cfg.Bridge.SourceID = value
	}
	if value, ok := lookupEnv("HERDR_DEVFLOW_PRIMARY_ROLE"); ok {
		cfg.Primary.Role = value
	}
	primaryModel, primaryModelSet := lookupEnv("HERDR_DEVFLOW_PRIMARY_MODEL")
	if value, ok := lookupEnv("HERDR_DEVFLOW_PRIMARY_KIND"); ok {
		if value != cfg.Primary.Kind && !primaryModelSet {
			cfg.Primary.Model = ""
		}
		cfg.Primary.Kind = value
	}
	if primaryModelSet {
		cfg.Primary.Model = primaryModel
	}
	if value, ok := lookupEnv("HERDR_DEVFLOW_RETRY_WINDOW"); ok {
		cfg.Scheduler.RetryWindow = value
	}
	return nil
}

// Version is a semver-like Herdr version. A prerelease suffix is preserved for
// display but comparison intentionally uses the stable numeric tuple.
type Version struct {
	Major int
	Minor int
	Patch int
	Raw   string
}

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+][0-9A-Za-z.-]+)?$`)

func ParseVersion(raw string) (Version, error) {
	trimmed := strings.TrimSpace(raw)
	matches := versionPattern.FindStringSubmatch(trimmed)
	if matches == nil {
		return Version{}, fmt.Errorf("must be a version such as 0.7.5")
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch, _ := strconv.Atoi(matches[3])
	return Version{Major: major, Minor: minor, Patch: patch, Raw: trimmed}, nil
}

func (v Version) AtLeast(other Version) bool {
	if v.Major != other.Major {
		return v.Major > other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor > other.Minor
	}
	return v.Patch >= other.Patch
}

// validate bounds every Overnight default. An invalid value here must fail the
// whole configuration rather than be quietly replaced: a run built on a
// silently corrected deadline would sleep the Mac against a boundary the user
// never chose.
func (o OvernightConfig) validate() error {
	if o.StartTime != "now" {
		if _, err := ParseClockTime(o.StartTime); err != nil {
			return fmt.Errorf("overnight.start_time must be \"now\" or HH:MM")
		}
	}
	if _, err := ParseClockTime(o.Deadline); err != nil {
		return fmt.Errorf("overnight.deadline must be HH:MM")
	}
	if o.Timezone != "" {
		if _, err := time.LoadLocation(o.Timezone); err != nil {
			return fmt.Errorf("overnight.timezone must be an IANA time zone such as America/New_York")
		}
	}
	if o.MaxResumes < 1 || o.MaxResumes > MaxAllowedResumes {
		return fmt.Errorf("overnight.max_resumes must be between 1 and %d", MaxAllowedResumes)
	}
	lead, err := time.ParseDuration(o.WakeLead)
	if err != nil || lead < MinWakeLead || lead > MaxWakeLead {
		return fmt.Errorf("overnight.wake_lead must be a Go duration between %s and %s", MinWakeLead, MaxWakeLead)
	}
	return nil
}

// ClockTime is an hour and minute on an unspecified day.
type ClockTime struct {
	Hour   int
	Minute int
}

var clockPattern = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)

// ParseClockTime reads an HH:MM local time.
func ParseClockTime(raw string) (ClockTime, error) {
	matches := clockPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if matches == nil {
		return ClockTime{}, fmt.Errorf("must be a 24-hour time such as 07:00")
	}
	hour, _ := strconv.Atoi(matches[1])
	minute, _ := strconv.Atoi(matches[2])
	return ClockTime{Hour: hour, Minute: minute}, nil
}

// WakeLead is the effective lead time before a reset.
func (c Config) WakeLead() time.Duration {
	duration, err := time.ParseDuration(c.Overnight.WakeLead)
	if err != nil {
		return 2 * time.Minute
	}
	return duration
}
