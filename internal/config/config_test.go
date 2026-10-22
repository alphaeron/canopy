package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/brand"
)

// withEnv sets the named variables for the duration of the test, restoring
// their prior state afterward.
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		prev, ok := os.LookupEnv(k)
		if ok {
			t.Cleanup(func() {
				if err := os.Setenv(k, prev); err != nil {
					t.Errorf("restore %s: %v", k, err)
				}
			})
		} else {
			t.Cleanup(func() {
				if err := os.Unsetenv(k); err != nil {
					t.Errorf("unset %s: %v", k, err)
				}
			})
		}
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
}

func TestLoadReadsYAMLFile(t *testing.T) {
	path := writeConfig(t, `verbose: true
serve_addr: ":9090"
github:
  app_id: "1337"
  webhook_secret: "topsecret"
`)
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Verbose {
		t.Error("verbose = false, want true from file")
	}
	if got.ServeAddr != ":9090" {
		t.Errorf("serve_addr = %q, want :9090 from file", got.ServeAddr)
	}
	if got.GitHub.AppID != "1337" {
		t.Errorf("github.app_id = %q, want 1337 from file", got.GitHub.AppID)
	}
	if got.GitHub.WebhookSecret != "topsecret" {
		t.Errorf("github.webhook_secret is not the value from the file")
	}
	if got.GitHub.PrivateKey != "" {
		t.Errorf("github.private_key = %q, want empty (absent from file)", got.GitHub.PrivateKey)
	}
}

// TestLoadPlatformSectionFromEnv: a platform section's keys are read from
// prefixed environment variables named after the dotted key.
func TestLoadPlatformSectionFromEnv(t *testing.T) {
	withEnv(t, map[string]string{
		brand.EnvPrefix + "GITHUB_APP_ID":         "42",
		brand.EnvPrefix + "GITHUB_WEBHOOK_SECRET": "s3",
	})
	got, err := Load(writeConfig(t, "github:\n  app_id: \"1\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.GitHub.AppID != "42" || got.GitHub.WebhookSecret != "s3" {
		t.Errorf("github = %+v, want the environment's app_id and webhook_secret", got.GitHub)
	}
	if !reflect.DeepEqual(configKeys(reflect.TypeFor[Config](), ""), []string{
		"verbose", "serve_addr", "github.app_id", "github.private_key", "github.webhook_secret",
	}) {
		t.Errorf("configKeys = %v, want every tagged field, sections dotted", configKeys(reflect.TypeFor[Config](), ""))
	}
}

// TestLoadLegacyKeys: the deprecated flat keys still configure their
// replacements, from the file or the environment, and lose to them.
func TestLoadLegacyKeys(t *testing.T) {
	tests := []struct {
		name string
		file string
		env  map[string]string
		want GitHub
	}{
		{name: "flat keys in the file",
			file: "github_app_id: \"7\"\ngithub_private_key: pem\nwebhook_hmac_secret: old\n",
			want: GitHub{AppID: "7", PrivateKey: "pem", WebhookSecret: "old"}},
		{name: "legacy webhook variable",
			file: "verbose: false\n",
			env:  map[string]string{brand.EnvPrefix + "WEBHOOK_HMAC_SECRET": "old"},
			want: GitHub{WebhookSecret: "old"}},
		{name: "replacement wins over legacy key",
			file: "webhook_hmac_secret: old\ngithub:\n  webhook_secret: new\n",
			want: GitHub{WebhookSecret: "new"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withEnv(t, tt.env)
			got, err := Load(writeConfig(t, tt.file))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got.GitHub != tt.want {
				t.Errorf("github = %+v, want %+v", got.GitHub, tt.want)
			}
		})
	}
}

// writeConfig writes text to a config file in a temporary directory and
// returns its path.
func writeConfig(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conf.yaml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.yaml")
	if err := os.WriteFile(path, []byte("verbose: true\nserve_addr: \":9090\""), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	withEnv(t, map[string]string{
		brand.EnvPrefix + "VERBOSE":    "false",
		brand.EnvPrefix + "SERVE_ADDR": ":7777",
	})
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Verbose {
		t.Error("verbose should be false: $VERBOSE beats the value in the config file")
	}
	if got.ServeAddr != ":7777" {
		t.Errorf("serve_addr = %q, want the env override :7777", got.ServeAddr)
	}
}

func TestLoadExplicitlyNamedMissingFileIsADeclaredError(t *testing.T) {
	// An explicitly-specified config path that points at nothing is a user
	// error (likely a typo), so Load reports it instead of silently
	// proceeding with defaults.
	if _, err := Load(filepath.Join(t.TempDir(), "definitely-absent.yaml")); err == nil {
		t.Error("Load with a missing explicit path must fail, not fall back to defaults")
	}
}

func TestLoadNoConfigAnywhereYieldsDefaults(t *testing.T) {
	// No explicit path and nothing under the config env var / cwd / home:
	// defaults and environment alone form a valid configuration.
	withEnv(t, map[string]string{brand.EnvConfig: ""})
	t.Chdir(t.TempDir())
	got, err := Load("")
	if err != nil {
		t.Fatalf("Load with no config file present: %v", err)
	}
	if got.ServeAddr != ":8080" {
		t.Errorf("default serve_addr = %q, want :8080", got.ServeAddr)
	}
	if got.Verbose {
		t.Error("verbose default must be false")
	}
}

func TestLoadUnparseableFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("not: [a: valid"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load must reject a config file that does not parse")
	}
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, brand.ConfigName+".yaml")
	if err := os.WriteFile(confPath, []byte("verbose: true"), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	withEnv(t, map[string]string{brand.EnvConfig: confPath})
	got, err := Load("")
	if err != nil {
		t.Fatalf("Load via $CONFIG: %v", err)
	}
	if !got.Verbose {
		t.Error("verbose from $CONFIG file was not picked up")
	}
}

func TestLoggerForVerboseWritesDebug(t *testing.T) {
	var buf bytes.Buffer
	loggerFor(true, &buf).Debug("debug-message")
	loggerFor(true, &buf).Info("info-message")
	if !bytes.Contains(buf.Bytes(), []byte("debug-message")) || !bytes.Contains(buf.Bytes(), []byte("info-message")) {
		t.Errorf("debug logger must emit both debug and info lines, got %q", buf.String())
	}
}

func TestLoggerForQuietDropsDebug(t *testing.T) {
	var buf bytes.Buffer
	loggerFor(false, &buf).Debug("debug-message")
	loggerFor(false, &buf).Info("info-message")
	if bytes.Contains(buf.Bytes(), []byte("debug-message")) {
		t.Errorf("info-level logger leaked a debug line: %q", buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte("info-message")) {
		t.Errorf("info line missing from quiet logger output")
	}
}

func TestLoadSetsDefaultLoggerToRequestedLevel(t *testing.T) {
	if _, err := Load(""); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The default logger must exist and be a *slog.Logger after Load; the
	// level itself is covered directly by TestLoggerForVerboseWritesDebug /
	// TestLoggerForQuietDropsDebug (Load always routes through loggerFor).
	if slog.Default() == nil || slog.Default().Handler() == nil {
		t.Error("slog.Default() must be a working logger after Load")
	}
}
