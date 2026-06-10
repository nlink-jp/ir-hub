// Package config loads ir-hub configuration from a TOML file with
// IRHUB_* environment-variable overrides.
//
// Precedence (lowest to highest): built-in defaults → TOML file →
// environment variables. Decoding is strict: unknown keys in the
// TOML file are an error, so typos fail fast instead of being
// silently ignored. Slack tokens are environment-only and must
// never appear in the config file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Defaults. Keep in sync with config.example.toml.
const (
	DefaultLocation        = "us-central1"
	DefaultModel           = "gemini-2.5-flash"
	DefaultVisibility      = "private"
	DefaultNamePrefix      = "ir-"
	DefaultGroupCacheTTL   = 300
	DefaultStorageBackend  = "local"
	DefaultStorageLocal    = "./knowledge"
	defaultDBPathUnderHome = ".local/share/ir-hub/ir-hub.db"
)

// Config is the root configuration. The [gcp], [model] and [storage]
// sections are loaded and validated from Phase 1 on, but only
// consumed by the LLM / export features of later phases.
type Config struct {
	GCP     GCPConfig     `toml:"gcp"`
	Model   ModelConfig   `toml:"model"`
	Channel ChannelConfig `toml:"channel"`
	ACL     ACLConfig     `toml:"acl"`
	Storage StorageConfig `toml:"storage"`
	DB      DBConfig      `toml:"db"`
	Slack   SlackConfig   `toml:"slack"`

	// Warnings collects non-fatal findings from Load (e.g. insecure
	// config-file permissions). The caller decides where to print.
	Warnings []string `toml:"-"`
}

type GCPConfig struct {
	Project  string `toml:"project"`
	Location string `toml:"location"`
}

type ModelConfig struct {
	Name string `toml:"name"`
}

type ChannelConfig struct {
	DefaultVisibility string `toml:"default_visibility"`
	NamePrefix        string `toml:"name_prefix"`
}

type ACLConfig struct {
	AllowUsers    []string `toml:"allow_users"`
	AllowGroups   []string `toml:"allow_groups"`
	DenyUsers     []string `toml:"deny_users"`
	DenyGroups    []string `toml:"deny_groups"`
	GroupCacheTTL int      `toml:"group_cache_ttl"`
	NotifyDenied  bool     `toml:"notify_denied"`
}

type StorageConfig struct {
	Backend   string `toml:"backend"`
	LocalPath string `toml:"local_path"`
	GCSBucket string `toml:"gcs_bucket"`
	S3Bucket  string `toml:"s3_bucket"`
	S3Prefix  string `toml:"s3_prefix"`
}

type DBConfig struct {
	Path string `toml:"path"`
}

// SlackConfig holds the Slack tokens. They may live in the config
// file (which should then be chmod 600 — Load warns otherwise) or
// in the environment; IRHUB_SLACK_*_TOKEN overrides the file.
type SlackConfig struct {
	AppToken string `toml:"app_token"`
	BotToken string `toml:"bot_token"`
}

// DefaultPath returns the default config file location.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".config", "ir-hub", "config.toml")
}

func defaults() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		GCP:     GCPConfig{Location: DefaultLocation},
		Model:   ModelConfig{Name: DefaultModel},
		Channel: ChannelConfig{DefaultVisibility: DefaultVisibility, NamePrefix: DefaultNamePrefix},
		ACL:     ACLConfig{GroupCacheTTL: DefaultGroupCacheTTL},
		Storage: StorageConfig{Backend: DefaultStorageBackend, LocalPath: DefaultStorageLocal},
		DB:      DBConfig{Path: filepath.Join(home, filepath.FromSlash(defaultDBPathUnderHome))},
	}
}

// Load reads the config file at path (DefaultPath() if path is
// empty), applies environment overrides, and validates the result.
// A missing file is not an error — defaults + env apply.
func Load(path string) (*Config, error) {
	cfg := defaults()

	if path == "" {
		path = DefaultPath()
	}
	if info, err := os.Stat(path); err == nil {
		// The file may contain credentials ([slack] tokens): warn
		// when group/other can read it (org convention).
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf(
				"Warning: config file %s has permissions %04o; expected 0600.\n"+
					"  The file may contain credentials. Run: chmod 600 %s",
				path, perm, path))
		}
		md, err := toml.DecodeFile(path, cfg)
		if err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
		if undecoded := md.Undecoded(); len(undecoded) > 0 {
			keys := make([]string, len(undecoded))
			for i, k := range undecoded {
				keys[i] = k.String()
			}
			return nil, fmt.Errorf("config %s: unknown keys (typo?): %s",
				path, strings.Join(keys, ", "))
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat config %s: %w", path, err)
	}

	applyEnvOverrides(cfg)
	expandDBPath(cfg)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	setStr := func(dst *string, key string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	setList := func(dst *[]string, key string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = splitList(v)
		}
	}

	setStr(&cfg.GCP.Project, "IRHUB_GCP_PROJECT")
	setStr(&cfg.GCP.Location, "IRHUB_GCP_LOCATION")
	setStr(&cfg.Model.Name, "IRHUB_MODEL_NAME")
	setStr(&cfg.Channel.DefaultVisibility, "IRHUB_CHANNEL_DEFAULT_VISIBILITY")
	setStr(&cfg.Channel.NamePrefix, "IRHUB_CHANNEL_NAME_PREFIX")
	setList(&cfg.ACL.AllowUsers, "IRHUB_ACL_ALLOW_USERS")
	setList(&cfg.ACL.AllowGroups, "IRHUB_ACL_ALLOW_GROUPS")
	setList(&cfg.ACL.DenyUsers, "IRHUB_ACL_DENY_USERS")
	setList(&cfg.ACL.DenyGroups, "IRHUB_ACL_DENY_GROUPS")
	parseIntEnv(&cfg.ACL.GroupCacheTTL, "IRHUB_ACL_GROUP_CACHE_TTL")
	parseBoolEnv(&cfg.ACL.NotifyDenied, "IRHUB_ACL_NOTIFY_DENIED")
	setStr(&cfg.Storage.Backend, "IRHUB_STORAGE_BACKEND")
	setStr(&cfg.Storage.LocalPath, "IRHUB_STORAGE_LOCAL_PATH")
	setStr(&cfg.DB.Path, "IRHUB_DB_PATH")
	setStr(&cfg.Slack.AppToken, "IRHUB_SLACK_APP_TOKEN")
	setStr(&cfg.Slack.BotToken, "IRHUB_SLACK_BOT_TOKEN")
}

// parseIntEnv overrides *dst only when the variable holds a valid
// integer; malformed values are ignored rather than zero-filling.
func parseIntEnv(dst *int, key string) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	if n, err := strconv.Atoi(v); err == nil {
		*dst = n
	}
}

func parseBoolEnv(dst *bool, key string) {
	v := os.Getenv(key)
	if v == "" {
		return
	}
	if b, err := strconv.ParseBool(v); err == nil {
		*dst = b
	}
}

// splitList parses a comma-separated env value into a trimmed,
// empty-stripped slice. "a, b,," → ["a" "b"].
func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func expandDBPath(cfg *Config) {
	p := cfg.DB.Path
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			cfg.DB.Path = filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
		}
	}
}

var namePrefixRe = regexp.MustCompile(`^[a-z0-9-]+$`)

func (c *Config) validate() error {
	switch c.Channel.DefaultVisibility {
	case "public", "private":
	default:
		return fmt.Errorf("channel.default_visibility must be \"public\" or \"private\", got %q",
			c.Channel.DefaultVisibility)
	}
	if !namePrefixRe.MatchString(c.Channel.NamePrefix) {
		return fmt.Errorf("channel.name_prefix must match [a-z0-9-]+, got %q", c.Channel.NamePrefix)
	}
	switch c.Storage.Backend {
	case "local", "gcs", "s3":
	default:
		return fmt.Errorf("storage.backend must be \"local\", \"gcs\" or \"s3\", got %q", c.Storage.Backend)
	}
	if c.ACL.GroupCacheTTL <= 0 {
		return fmt.Errorf("acl.group_cache_ttl must be positive, got %d", c.ACL.GroupCacheTTL)
	}
	if c.DB.Path == "" {
		return fmt.Errorf("db.path must not be empty")
	}
	return nil
}

// ValidateServe checks the requirements that only the resident bot
// needs: both Slack tokens present and well-formed.
func (c *Config) ValidateServe() error {
	if c.Slack.AppToken == "" {
		return fmt.Errorf("IRHUB_SLACK_APP_TOKEN is required (app-level token for Socket Mode)")
	}
	if !strings.HasPrefix(c.Slack.AppToken, "xapp-") {
		return fmt.Errorf("IRHUB_SLACK_APP_TOKEN must start with \"xapp-\"")
	}
	if c.Slack.BotToken == "" {
		return fmt.Errorf("IRHUB_SLACK_BOT_TOKEN is required (bot token)")
	}
	if !strings.HasPrefix(c.Slack.BotToken, "xoxb-") {
		return fmt.Errorf("IRHUB_SLACK_BOT_TOKEN must start with \"xoxb-\"")
	}
	return nil
}
