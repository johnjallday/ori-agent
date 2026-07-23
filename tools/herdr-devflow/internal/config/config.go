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
}

type BridgeConfig struct {
	SchemaVersion   int    `toml:"schema_version"`
	Enabled         bool   `toml:"enabled"`
	MinHerdrVersion string `toml:"min_herdr_version"`
	SourceID        string `toml:"source_id"`
}

type PrimaryConfig struct {
	Role string `toml:"role"`
	Kind string `toml:"kind"`
}

type RolesConfig struct {
	DefaultKind string            `toml:"default_kind"`
	Defaults    map[string]string `toml:"defaults"`
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
}

func Default() Config {
	return Config{
		Bridge: BridgeConfig{
			SchemaVersion:   1,
			Enabled:         true,
			MinHerdrVersion: "0.7.5",
			SourceID:        "ori.devflow",
		},
		Primary: PrimaryConfig{Role: "builder", Kind: "claude"},
		Roles: RolesConfig{
			DefaultKind: "claude",
			Defaults: map[string]string{
				"reviewer": "claude",
				"tester":   "claude",
			},
		},
		Bootstrap: BootstrapConfig{Template: "primary-v1", TimeoutSeconds: 30},
		Scheduler: SchedulerConfig{RetryWindow: "15m"},
		Metadata:  MetadataConfig{Enabled: true},
		Status:    StatusConfig{WatchPollInterval: "2s"},
	}
}

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
	if !supportedAgentKind(c.Primary.Kind) {
		return fmt.Errorf("primary.kind must be a Herdr-supported agent kind")
	}
	if !supportedAgentKind(c.Roles.DefaultKind) {
		return fmt.Errorf("roles.default_kind must be a Herdr-supported agent kind")
	}
	for role, kind := range c.Roles.Defaults {
		if !identifierPattern.MatchString(role) {
			return fmt.Errorf("roles.defaults.%s must match %s", role, identifierPattern.String())
		}
		if !supportedAgentKind(kind) {
			return fmt.Errorf("roles.defaults.%s must be a Herdr-supported agent kind", role)
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
	return nil
}

func supportedAgentKind(kind string) bool {
	if !identifierPattern.MatchString(kind) {
		return false
	}
	_, ok := supportedAgentKinds[kind]
	return ok
}

func (c Config) RoleKind(role string) string {
	if role == c.Primary.Role {
		return c.Primary.Kind
	}
	if kind, ok := c.Roles.Defaults[role]; ok {
		return kind
	}
	return c.Roles.DefaultKind
}

func (c Config) RetryWindow() time.Duration {
	duration, _ := time.ParseDuration(c.Scheduler.RetryWindow)
	return duration
}

func (c Config) WatchPollInterval() time.Duration {
	duration, _ := time.ParseDuration(c.Status.WatchPollInterval)
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
	if value, ok := lookupEnv("HERDR_DEVFLOW_PRIMARY_KIND"); ok {
		cfg.Primary.Kind = value
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
