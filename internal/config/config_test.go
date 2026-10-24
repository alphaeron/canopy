package config

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alphaeron/canopy/internal/brand"
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
		"verbose", "serve_addr", "review_timeout", "report_timeout", "review_workers", "review_queue_per_worker", "shutdown_timeout", "database_url", "database_key",
		"public_url", "link_key", "link_lifetime", "blocking_labels",
		"github.app_id", "github.private_key", "github.webhook_secret", "github.api_url", "github.request_timeout",
	}) {
		t.Errorf("configKeys = %v, want every tagged field, sections dotted", configKeys(reflect.TypeFor[Config](), ""))
	}
}

// TestLoadIgnoresFlatGitHubKeys pins that only the github section
// configures the GitHub platform: the old flat keys are not read.
func TestLoadIgnoresFlatGitHubKeys(t *testing.T) {
	withEnv(t, map[string]string{brand.EnvPrefix + "WEBHOOK_HMAC_SECRET": "old"})
	got, err := Load(writeConfig(t, "github_app_id: \"7\"\ngithub_private_key: pem\nwebhook_hmac_secret: old\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.GitHub != (GitHub{}) {
		t.Errorf("github = %+v, want empty: flat keys are not supported", got.GitHub)
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

func TestLoggerForWritesJSON(t *testing.T) {
	var buf bytes.Buffer
	loggerFor(false, &buf).Info("info-message", "key", "value")
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	if got["msg"] != "info-message" || got["key"] != "value" || got["level"] != "INFO" {
		t.Errorf("unexpected JSON log record: %v", got)
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

// testKeyPEM is a fresh RSA key in PKCS #1 PEM.
func testKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func TestGitHubValidate(t *testing.T) {
	key := testKeyPEM(t)
	valid := GitHub{AppID: "12", PrivateKey: key, WebhookSecret: "s"}
	tests := []struct {
		name string
		g    GitHub
		want []string // substrings of the error; none means valid
	}{
		{name: "complete", g: valid},
		{name: "enterprise URL", g: GitHub{AppID: "12", PrivateKey: key, WebhookSecret: "s", APIURL: "https://ghe.example.com/api/v3"}},
		{name: "missing keys", g: GitHub{AppID: "12"}, want: []string{"github.private_key is required", "github.webhook_secret is required"}},
		{name: "non-numeric app id", g: GitHub{AppID: "abc", PrivateKey: key, WebhookSecret: "s"}, want: []string{"app_id must be a positive integer"}},
		{name: "key not PEM", g: GitHub{AppID: "12", PrivateKey: "not a key", WebhookSecret: "s"}, want: []string{"private_key is not PEM"}},
		{name: "relative API URL", g: GitHub{AppID: "12", PrivateKey: key, WebhookSecret: "s", APIURL: "/api"}, want: []string{"api_url must be an absolute"}},
		{name: "http API URL", g: GitHub{AppID: "12", PrivateKey: key, WebhookSecret: "s", APIURL: "http://ghe.example.com/api/v3"}, want: []string{"api_url must be an absolute https URL"}},
		{name: "other scheme API URL", g: GitHub{AppID: "12", PrivateKey: key, WebhookSecret: "s", APIURL: "ftp://ghe.example.com/"}, want: []string{"api_url must be an absolute https URL"}},
		{name: "http on localhost", g: GitHub{AppID: "12", PrivateKey: key, WebhookSecret: "s", APIURL: "http://localhost:8080/"}},
		{name: "http on loopback IPv4", g: GitHub{AppID: "12", PrivateKey: key, WebhookSecret: "s", APIURL: "http://127.0.0.1:8080/"}},
		{name: "http on loopback IPv6", g: GitHub{AppID: "12", PrivateKey: key, WebhookSecret: "s", APIURL: "http://[::1]:8080/"}},
		{name: "http on a host named like loopback", g: GitHub{AppID: "12", PrivateKey: key, WebhookSecret: "s", APIURL: "http://127.0.0.1.example.com/"}, want: []string{"api_url must be an absolute https URL"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.g.Validate()
			if len(tt.want) == 0 {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate = nil, want %v", tt.want)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("Validate = %q, want it to contain %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "not a key") {
				t.Errorf("Validate = %q quotes the private key", err)
			}
		})
	}
}

func TestGitHubEnabledAndBaseURL(t *testing.T) {
	if (GitHub{}).Enabled() {
		t.Error("empty section Enabled = true")
	}
	if !(GitHub{WebhookSecret: "s"}).Enabled() {
		t.Error("section with a key Enabled = false")
	}
	if got := (GitHub{}).BaseURL(); got != DefaultGitHubAPIURL {
		t.Errorf("BaseURL = %q, want %q", got, DefaultGitHubAPIURL)
	}
	if got := (GitHub{APIURL: "https://ghe/api/v3"}).BaseURL(); got != "https://ghe/api/v3/" {
		t.Errorf("BaseURL = %q, want a trailing slash", got)
	}
	if (GitHub{RequestTimeout: time.Second}).Enabled() {
		t.Error("section with only request_timeout Enabled = true")
	}
	if got := (GitHub{}).APITimeout(); got != DefaultGitHubRequestTimeout {
		t.Errorf("APITimeout = %v, want %v", got, DefaultGitHubRequestTimeout)
	}
	if got := (GitHub{RequestTimeout: time.Second}).APITimeout(); got != time.Second {
		t.Errorf("APITimeout = %v, want the configured 1s", got)
	}
}

func TestSelectedPlatform(t *testing.T) {
	complete := GitHub{AppID: "1", PrivateKey: testKeyPEM(t), WebhookSecret: "s"}
	tests := []struct {
		name    string
		cfg     Config
		want    Platform
		wantErr bool
	}{
		{name: "nothing configured", cfg: Config{}},
		{name: "github section configured", cfg: Config{GitHub: complete}, want: PlatformGitHub},
		{name: "only the app id", cfg: Config{GitHub: GitHub{AppID: "1"}}, wantErr: true},
		{name: "only the api url", cfg: Config{GitHub: GitHub{APIURL: "https://ghe/api/v3/"}}, wantErr: true},
		{name: "all but the webhook secret", cfg: Config{GitHub: GitHub{AppID: "1", PrivateKey: complete.PrivateKey}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.SelectedPlatform()
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("SelectedPlatform = %q, %v; want %q, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

// TestSelectedPlatformFromTheEnvironment: the deployment's environment
// alone selects the platform, with no platform key and no config file
// settings.
func TestSelectedPlatformFromTheEnvironment(t *testing.T) {
	t.Run("none configured", func(t *testing.T) {
		cfg, err := Load(writeConfig(t, "verbose: false\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got, err := cfg.SelectedPlatform(); got != "" || err != nil {
			t.Errorf("SelectedPlatform = %q, %v; want none", got, err)
		}
	})
	t.Run("github from its variables", func(t *testing.T) {
		withEnv(t, map[string]string{
			brand.EnvPrefix + "GITHUB_APP_ID":         "1",
			brand.EnvPrefix + "GITHUB_PRIVATE_KEY":    testKeyPEM(t),
			brand.EnvPrefix + "GITHUB_WEBHOOK_SECRET": "s",
		})
		cfg, err := Load(writeConfig(t, "verbose: false\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got, err := cfg.SelectedPlatform(); got != PlatformGitHub || err != nil {
			t.Errorf("SelectedPlatform = %q, %v; want %q", got, err, PlatformGitHub)
		}
	})
	t.Run("a partly configured platform is an error", func(t *testing.T) {
		withEnv(t, map[string]string{brand.EnvPrefix + "GITHUB_APP_ID": "1"})
		cfg, err := Load(writeConfig(t, "verbose: false\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if _, err := cfg.SelectedPlatform(); err == nil {
			t.Error("SelectedPlatform with only the app ID succeeded, want an error")
		}
	})
}

func TestLoadServerSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got, err := Load(writeConfig(t, "verbose: false\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got.ReviewTimeout != DefaultReviewTimeout || got.ReportTimeout != DefaultReportTimeout ||
			got.ReviewWorkers != DefaultReviewWorkers || got.ReviewQueuePerWorker != DefaultReviewQueuePerWorker ||
			got.ShutdownTimeout != DefaultShutdownTimeout || got.LinkLifetime != DefaultLinkLifetime ||
			got.GitHub.APITimeout() != DefaultGitHubRequestTimeout {
			t.Errorf("config = %+v; want the server defaults", got)
		}
	})
	t.Run("from the environment", func(t *testing.T) {
		withEnv(t, map[string]string{
			brand.EnvPrefix + "REVIEW_TIMEOUT":          "30s",
			brand.EnvPrefix + "REVIEW_WORKERS":          "9",
			brand.EnvPrefix + "REVIEW_QUEUE_PER_WORKER": "3",
			brand.EnvPrefix + "SHUTDOWN_TIMEOUT":        "5s",
			brand.EnvPrefix + "REPORT_TIMEOUT":          "7s",
			brand.EnvPrefix + "LINK_LIFETIME":           "168h",
			brand.EnvPrefix + "GITHUB_REQUEST_TIMEOUT":  "11s",
			brand.EnvPrefix + "GITHUB_API_URL":          "https://ghe/api/v3/",
		})
		got, err := Load(writeConfig(t, "verbose: false\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got.ReviewTimeout != 30*time.Second || got.ReviewWorkers != 9 || got.ReviewQueuePerWorker != 3 ||
			got.ShutdownTimeout != 5*time.Second || got.ReportTimeout != 7*time.Second ||
			got.LinkLifetime != 7*24*time.Hour ||
			got.GitHub.RequestTimeout != 11*time.Second ||
			got.GitHub.APIURL != "https://ghe/api/v3/" {
			t.Errorf("config = %+v, want the environment's server settings", got)
		}
	})
}

func TestLoadBlockingLabels(t *testing.T) {
	t.Run("from the file", func(t *testing.T) {
		got, err := Load(writeConfig(t, "blocking_labels:\n  - do not merge\n  - wip\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if want := []string{"do not merge", "wip"}; !reflect.DeepEqual(got.BlockingLabels, want) {
			t.Errorf("BlockingLabels = %q, want %q", got.BlockingLabels, want)
		}
	})
	t.Run("from the environment, comma-separated", func(t *testing.T) {
		withEnv(t, map[string]string{brand.EnvPrefix + "BLOCKING_LABELS": "do not merge,wip"})
		got, err := Load(writeConfig(t, "blocking_labels: [other]\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if want := []string{"do not merge", "wip"}; !reflect.DeepEqual(got.BlockingLabels, want) {
			t.Errorf("BlockingLabels = %q, want %q", got.BlockingLabels, want)
		}
	})
	t.Run("none by default", func(t *testing.T) {
		got, err := Load(writeConfig(t, "verbose: false\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(got.BlockingLabels) != 0 {
			t.Errorf("BlockingLabels = %q, want none", got.BlockingLabels)
		}
	})
}

func TestReviewQueue(t *testing.T) {
	tests := []struct {
		name               string
		workers, perWorker int
		want               int
		wantErr            bool
	}{
		{name: "defaults", workers: DefaultReviewWorkers, perWorker: DefaultReviewQueuePerWorker, want: 256},
		{name: "at the limit", workers: 4, perWorker: MaxReviewQueue / 4, want: MaxReviewQueue},
		{name: "over the limit", workers: 4, perWorker: MaxReviewQueue/4 + 1, wantErr: true},
		{name: "too many workers", workers: MaxReviewQueue + 1, perWorker: 1, wantErr: true},
		{name: "would overflow", workers: math.MaxInt, perWorker: math.MaxInt, wantErr: true},
		{name: "zero workers", workers: 0, perWorker: 16, wantErr: true},
		{name: "zero per worker", workers: 4, perWorker: 0, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{ReviewWorkers: tt.workers, ReviewQueuePerWorker: tt.perWorker}
			got, err := cfg.ReviewQueue()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ReviewQueue() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ReviewQueue() = %d, want %d", got, tt.want)
			}
		})
	}
}
