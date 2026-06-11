package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clearEnv isolates tests from the host environment: every IRHUB_*
// variable is unset and HOME points at a temp dir so user-level
// config files cannot leak in.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		key := strings.SplitN(kv, "=", 2)[0]
		if strings.HasPrefix(key, "IRHUB_") {
			t.Setenv(key, "")
			os.Unsetenv(key)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	os.Unsetenv("XDG_CONFIG_HOME")
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Channel.DefaultVisibility != "private" {
		t.Errorf("DefaultVisibility = %q, want private", cfg.Channel.DefaultVisibility)
	}
	if cfg.Channel.NamePrefix != "ir-" {
		t.Errorf("NamePrefix = %q, want ir-", cfg.Channel.NamePrefix)
	}
	if cfg.ACL.GroupCacheTTL != 300 {
		t.Errorf("GroupCacheTTL = %d, want 300", cfg.ACL.GroupCacheTTL)
	}
	if cfg.ACL.NotifyDenied {
		t.Error("NotifyDenied = true, want false")
	}
	if cfg.Storage.Backend != "local" {
		t.Errorf("Storage.Backend = %q, want local", cfg.Storage.Backend)
	}
	if cfg.GCP.Location != DefaultLocation {
		t.Errorf("GCP.Location = %q, want %q", cfg.GCP.Location, DefaultLocation)
	}
	if !strings.HasSuffix(cfg.DB.Path, filepath.FromSlash(".local/share/ir-hub/ir-hub.db")) {
		t.Errorf("DB.Path = %q, want default under home", cfg.DB.Path)
	}
}

func TestLoadTOML(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
[channel]
default_visibility = "public"
name_prefix = "inc-"

[acl]
allow_users = ["U111", "U222"]
allow_groups = ["ir-team"]
group_cache_ttl = 60
notify_denied = true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Channel.DefaultVisibility != "public" {
		t.Errorf("DefaultVisibility = %q, want public", cfg.Channel.DefaultVisibility)
	}
	if cfg.Channel.NamePrefix != "inc-" {
		t.Errorf("NamePrefix = %q, want inc-", cfg.Channel.NamePrefix)
	}
	if len(cfg.ACL.AllowUsers) != 2 || cfg.ACL.AllowUsers[0] != "U111" {
		t.Errorf("AllowUsers = %v", cfg.ACL.AllowUsers)
	}
	if cfg.ACL.GroupCacheTTL != 60 {
		t.Errorf("GroupCacheTTL = %d, want 60", cfg.ACL.GroupCacheTTL)
	}
	if !cfg.ACL.NotifyDenied {
		t.Error("NotifyDenied = false, want true")
	}
	// Unset sections keep defaults.
	if cfg.Storage.Backend != "local" {
		t.Errorf("Storage.Backend = %q, want local default", cfg.Storage.Backend)
	}
}

func TestLoadUnknownKeyFails(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
[channel]
default_visibilty = "public"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("Load with typo key: err = %v, want unknown-keys error", err)
	}
}

func TestLoadSlackTokensFromTOML(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
[gcp]
project = "example-project"

[slack]
app_token = "xapp-1-EXAMPLE"
bot_token = "xoxb-EXAMPLE"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Slack.AppToken != "xapp-1-EXAMPLE" || cfg.Slack.BotToken != "xoxb-EXAMPLE" {
		t.Errorf("Slack = %+v, want tokens from TOML", cfg.Slack)
	}
	if err := cfg.ValidateServe(); err != nil {
		t.Errorf("ValidateServe with TOML tokens: %v", err)
	}
	// 0600 file: no permission warning.
	if len(cfg.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none for 0600", cfg.Warnings)
	}
}

func TestEnvTokenBeatsTOML(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
[slack]
app_token = "xapp-1-FROMFILE"
bot_token = "xoxb-FROMFILE"
`)
	t.Setenv("IRHUB_SLACK_BOT_TOKEN", "xoxb-FROMENV")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Slack.BotToken != "xoxb-FROMENV" {
		t.Errorf("BotToken = %q, want env override", cfg.Slack.BotToken)
	}
	if cfg.Slack.AppToken != "xapp-1-FROMFILE" {
		t.Errorf("AppToken = %q, want file value kept", cfg.Slack.AppToken)
	}
}

func TestLoosePermissionsWarn(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
[channel]
default_visibility = "public"
`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want exactly one", cfg.Warnings)
	}
	w := cfg.Warnings[0]
	for _, want := range []string{"0644", "expected 0600", "chmod 600 " + path} {
		if !strings.Contains(w, want) {
			t.Errorf("warning missing %q:\n%s", want, w)
		}
	}
	// Loose permissions warn but never fail the load.
	if cfg.Channel.DefaultVisibility != "public" {
		t.Errorf("config not loaded alongside warning")
	}
}

func TestEnvBeatsTOML(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
[channel]
default_visibility = "public"
`)
	t.Setenv("IRHUB_CHANNEL_DEFAULT_VISIBILITY", "private")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Channel.DefaultVisibility != "private" {
		t.Errorf("DefaultVisibility = %q, want env override private", cfg.Channel.DefaultVisibility)
	}
}

func TestEnvListSplitting(t *testing.T) {
	clearEnv(t)
	t.Setenv("IRHUB_ACL_ALLOW_USERS", " U111 , U222,,U333 ")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"U111", "U222", "U333"}
	if len(cfg.ACL.AllowUsers) != len(want) {
		t.Fatalf("AllowUsers = %v, want %v", cfg.ACL.AllowUsers, want)
	}
	for i := range want {
		if cfg.ACL.AllowUsers[i] != want[i] {
			t.Errorf("AllowUsers[%d] = %q, want %q", i, cfg.ACL.AllowUsers[i], want[i])
		}
	}
}

func TestEnvMalformedIntKeepsExisting(t *testing.T) {
	clearEnv(t)
	t.Setenv("IRHUB_ACL_GROUP_CACHE_TTL", "not-a-number")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ACL.GroupCacheTTL != 300 {
		t.Errorf("GroupCacheTTL = %d, want default 300 kept on malformed env", cfg.ACL.GroupCacheTTL)
	}
}

func TestValidateVisibilityEnum(t *testing.T) {
	clearEnv(t)
	t.Setenv("IRHUB_CHANNEL_DEFAULT_VISIBILITY", "secret")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "default_visibility") {
		t.Errorf("Load with bad visibility: err = %v, want default_visibility error", err)
	}
}

func TestValidateBackendEnum(t *testing.T) {
	clearEnv(t)
	t.Setenv("IRHUB_STORAGE_BACKEND", "ftp")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "storage.backend") {
		t.Errorf("Load with bad backend: err = %v, want storage.backend error", err)
	}
}

func TestValidateNamePrefix(t *testing.T) {
	clearEnv(t)
	t.Setenv("IRHUB_CHANNEL_NAME_PREFIX", "IR_")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "name_prefix") {
		t.Errorf("Load with bad prefix: err = %v, want name_prefix error", err)
	}
}

func TestDBPathTildeExpansion(t *testing.T) {
	clearEnv(t)
	t.Setenv("IRHUB_DB_PATH", "~/custom/ir.db")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.HasPrefix(cfg.DB.Path, "~") {
		t.Errorf("DB.Path = %q, want tilde expanded", cfg.DB.Path)
	}
	if !strings.HasSuffix(cfg.DB.Path, filepath.FromSlash("custom/ir.db")) {
		t.Errorf("DB.Path = %q, want suffix custom/ir.db", cfg.DB.Path)
	}
}

func TestValidateServe(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.ValidateServe(); err == nil || !strings.Contains(err.Error(), "IRHUB_SLACK_APP_TOKEN") {
		t.Errorf("ValidateServe without tokens: err = %v, want app token error", err)
	}

	cfg.Slack.AppToken = "xoxb-wrong-kind" //nolint // placeholder, not a credential
	if err := cfg.ValidateServe(); err == nil || !strings.Contains(err.Error(), "xapp-") {
		t.Errorf("ValidateServe with bad app token: err = %v, want xapp- error", err)
	}

	cfg.Slack.AppToken = "xapp-1-EXAMPLE"
	if err := cfg.ValidateServe(); err == nil || !strings.Contains(err.Error(), "IRHUB_SLACK_BOT_TOKEN") {
		t.Errorf("ValidateServe without bot token: err = %v, want bot token error", err)
	}

	cfg.Slack.BotToken = "xoxb-EXAMPLE"
	if err := cfg.ValidateServe(); err == nil || !strings.Contains(err.Error(), "gcp.project") {
		t.Errorf("ValidateServe without project: err = %v, want gcp.project error", err)
	}

	cfg.GCP.Project = "example-project"
	if err := cfg.ValidateServe(); err != nil {
		t.Errorf("ValidateServe fully configured: err = %v, want nil", err)
	}
}

func TestValidateServeStorage(t *testing.T) {
	base := func() *Config {
		c := defaults()
		c.Slack = SlackConfig{AppToken: "xapp-1-X", BotToken: "xoxb-X"}
		c.GCP.Project = "p"
		return c
	}

	gcs := base()
	gcs.Storage.Backend = "gcs"
	if err := gcs.ValidateServe(); err == nil || !strings.Contains(err.Error(), "gcs_bucket") {
		t.Errorf("gcs without bucket: err = %v", err)
	}
	gcs.Storage.GCSBucket = "my-bucket"
	if err := gcs.ValidateServe(); err != nil {
		t.Errorf("gcs with bucket: %v", err)
	}

	s3 := base()
	s3.Storage.Backend = "s3"
	if err := s3.ValidateServe(); err == nil || !strings.Contains(err.Error(), "s3_bucket") {
		t.Errorf("s3 without bucket: err = %v", err)
	}
	s3.Storage.S3Bucket = "my-bucket"
	if err := s3.ValidateServe(); err != nil {
		t.Errorf("s3 with bucket: %v", err)
	}

	// local needs nothing extra.
	if err := base().ValidateServe(); err != nil {
		t.Errorf("local backend: %v", err)
	}
}

func TestStorageEnvOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("IRHUB_STORAGE_BACKEND", "s3")
	t.Setenv("IRHUB_STORAGE_S3_BUCKET", "ir-knowledge")
	t.Setenv("IRHUB_STORAGE_S3_PREFIX", "exports/")
	t.Setenv("IRHUB_STORAGE_GCS_BUCKET", "gcs-knowledge")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Storage.Backend != "s3" || cfg.Storage.S3Bucket != "ir-knowledge" ||
		cfg.Storage.S3Prefix != "exports/" || cfg.Storage.GCSBucket != "gcs-knowledge" {
		t.Errorf("storage = %+v", cfg.Storage)
	}
}

func TestAnalysisDefaultsAndEnv(t *testing.T) {
	clearEnv(t)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Analysis.RequestTimeout != DefaultRequestTimeout {
		t.Errorf("RequestTimeout = %d, want %d", cfg.Analysis.RequestTimeout, DefaultRequestTimeout)
	}
	if cfg.Analysis.MaxInputTokens != DefaultMaxInputTokens {
		t.Errorf("MaxInputTokens = %d, want %d", cfg.Analysis.MaxInputTokens, DefaultMaxInputTokens)
	}

	t.Setenv("IRHUB_ANALYSIS_REQUEST_TIMEOUT", "300")
	t.Setenv("IRHUB_ANALYSIS_MAX_INPUT_TOKENS", "50000")
	cfg, err = Load("")
	if err != nil {
		t.Fatalf("Load with env: %v", err)
	}
	if cfg.Analysis.RequestTimeout != 300 || cfg.Analysis.MaxInputTokens != 50000 {
		t.Errorf("Analysis = %+v, want env overrides", cfg.Analysis)
	}
}

func TestAnalysisValidation(t *testing.T) {
	clearEnv(t)
	t.Setenv("IRHUB_ANALYSIS_REQUEST_TIMEOUT", "-1")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "request_timeout") {
		t.Errorf("Load with negative timeout: err = %v", err)
	}
}

func TestAnalysisFromTOML(t *testing.T) {
	clearEnv(t)
	path := writeConfig(t, `
[analysis]
request_timeout = 240
max_input_tokens = 100000
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Analysis.RequestTimeout != 240 || cfg.Analysis.MaxInputTokens != 100000 {
		t.Errorf("Analysis = %+v", cfg.Analysis)
	}
}

func TestTokensFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("IRHUB_SLACK_APP_TOKEN", "xapp-1-EXAMPLE")
	t.Setenv("IRHUB_SLACK_BOT_TOKEN", "xoxb-EXAMPLE")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Slack.AppToken != "xapp-1-EXAMPLE" || cfg.Slack.BotToken != "xoxb-EXAMPLE" {
		t.Errorf("Slack tokens not loaded from env: %+v", cfg.Slack)
	}
}
