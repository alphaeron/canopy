// Package config is the single place that discovers and parses the shared
// configuration for every binary (the CLI and the review server).
// Both entry points call Load first thing in main() and pass the resulting
// *Config by value into the internal packages; no other package owns a
// package-level viper instance. The only documented side effect of Load is
// slog.SetDefault(), so every existing internal-package slog call picks up
// the configured verbosity without knowing about configuration at all.
package config

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/alphaeron/canopy/internal/brand"
)

// Config is the fully resolved configuration for one process. Fields are
// plain values read from the configuration file and the prefixed
// environment variables, where environment wins over file and file wins over
// defaults. Each hosted platform has its own section, so adding a platform
// adds a section rather than top-level keys. Every key is documented in the
// README's "Configuration" section so the same table serves deployment of
// the review server.
type Config struct {
	// Verbose selects debug-level structured logging; false selects info.
	Verbose bool `mapstructure:"verbose"`
	// ServeAddr is the review server's listen address; defaults to ":8080".
	ServeAddr string `mapstructure:"serve_addr"`
	// ReviewTimeout bounds one webhook-triggered review; defaults to 50
	// seconds.
	ReviewTimeout time.Duration `mapstructure:"review_timeout"`
	// ReportTimeout bounds reporting the outcome of a review that ran out
	// of time, which can no longer use the review's own deadline; defaults
	// to 30 seconds.
	ReportTimeout time.Duration `mapstructure:"report_timeout"`
	// ReviewWorkers is how many reviews the review server runs at once;
	// defaults to 4.
	ReviewWorkers int `mapstructure:"review_workers"`
	// ReviewQueuePerWorker is how many pull requests may wait per review
	// worker; defaults to 16. See ReviewQueue for the total.
	ReviewQueuePerWorker int `mapstructure:"review_queue_per_worker"`
	// ShutdownTimeout bounds how long the review server waits for open requests
	// once it is told to stop; defaults to 10 seconds.
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	// DatabaseURL is the review server's PostgreSQL connection string;
	// empty runs the server without a database. It carries credentials,
	// so it is never logged.
	DatabaseURL string `mapstructure:"database_url"`
	// DatabaseKey authenticates the platform data the review server caches
	// in the database (see store.Options.Key): at least 32 bytes, the same
	// on every replica, and never stored in the database. Required with
	// DatabaseURL. It is a secret, so it is never logged.
	DatabaseKey string `mapstructure:"database_key"`
	// PublicURL is the review server's URL as the platform's users reach
	// it, e.g. "https://canopy.example.com"; with LinkKey it serves review
	// pages, and each pull request's status links to its own. Empty
	// serves none.
	PublicURL string `mapstructure:"public_url"`
	// LinkKey signs the links to review pages (see
	// server.Options.LinkKey): at least 32 bytes and the same on every
	// replica. Required with PublicURL. It is a secret, so it is never
	// logged.
	LinkKey string `mapstructure:"link_key"`
	// LinkLifetime is how long a review page's link opens its page after
	// the server signs it (see server.Options.LinkLifetime); defaults to
	// 90 days. A change applies to links signed from then on: each link
	// carries its own expiry.
	LinkLifetime time.Duration `mapstructure:"link_lifetime"`
	// BlockingLabels are the labels that fail a change carrying any of
	// them, whatever its approvals, e.g. "do not merge"; compared
	// case-insensitively. The environment variable takes a comma-separated
	// list. Empty, the default, blocks on no label.
	BlockingLabels []string `mapstructure:"blocking_labels"`
	// GitHub configures the GitHub platform for the review server.
	GitHub GitHub `mapstructure:"github"`
	// Path is the file the configuration was read from, when one existed.
	// It is empty when defaults and environment alone formed the active
	// configuration. Callers log Path exactly once, replacing the old
	// "dump every key" behavior.
	Path string `mapstructure:"-"`
}

// GitHub is the GitHub platform's section: the GitHub App the review
// server acts as. The CLI never reads it.
type GitHub struct {
	// AppID is the numeric GitHub App ID, kept as a string so the file and
	// environment supply exactly the same value with no numeric coercion
	// surprises.
	AppID string `mapstructure:"app_id"`
	// PrivateKey is the GitHub App RSA private key (PEM), used to mint
	// installation tokens. Never logged.
	PrivateKey string `mapstructure:"private_key"`
	// WebhookSecret is the webhook signing secret; webhook signatures that
	// do not verify against it are rejected. Never logged.
	WebhookSecret string `mapstructure:"webhook_secret"`
	// APIURL is the REST API's base URL, for GitHub Enterprise Server
	// (e.g. "https://github.example.com/api/v3/"); empty means
	// DefaultGitHubAPIURL. It must be https, except on a loopback host:
	// every request to it carries the app's credentials.
	APIURL string `mapstructure:"api_url"`
	// RequestTimeout bounds waiting for each REST API response's headers,
	// so one stuck request cannot hold a review until its own timeout;
	// zero or negative means DefaultGitHubRequestTimeout. Setting it alone
	// does not enable the section.
	RequestTimeout time.Duration `mapstructure:"request_timeout"`
}

// Defaults for the GitHub platform's settings.
const (
	// DefaultGitHubAPIURL is github.com's REST API.
	DefaultGitHubAPIURL         = "https://api.github.com/"
	DefaultGitHubRequestTimeout = 30 * time.Second
)

// Defaults for the review server's settings.
const (
	DefaultServeAddr            = ":8080"
	DefaultReviewTimeout        = 50 * time.Second
	DefaultReportTimeout        = 30 * time.Second
	DefaultReviewWorkers        = 4
	DefaultReviewQueuePerWorker = 16
	DefaultShutdownTimeout      = 10 * time.Second
	DefaultLinkLifetime         = 90 * 24 * time.Hour
)

// MaxReviewQueue caps ReviewQueue, the pull requests that may wait for a
// review worker, so a misconfigured server cannot reserve an unbounded queue.
const MaxReviewQueue = 4096

// Platform is a hosted platform the review server can review on, as
// SelectedPlatform returns it. Its value is also the platform's webhook
// route segment. The zero value means no platform is configured.
type Platform string

// The platforms the review server can review on.
const (
	PlatformGitHub Platform = "github"
)

// Enabled reports whether any GitHub key is set: the deployment means to
// review on GitHub, so the section must then Validate.
func (g GitHub) Enabled() bool {
	return g.AppID != "" || g.PrivateKey != "" || g.WebhookSecret != "" || g.APIURL != ""
}

// Validate checks that the section can run the GitHub platform: app_id,
// private_key, and webhook_secret are all set, app_id is a positive
// integer, private_key is an RSA private key in PEM, and api_url (when set)
// is an absolute https URL, or http on a loopback host. Errors name keys,
// never secret values. Load does not call it, so the CLI never fails on
// server settings; the review server does, at startup.
func (g GitHub) Validate() error {
	var errs []error
	for key, v := range map[string]string{"app_id": g.AppID, "private_key": g.PrivateKey, "webhook_secret": g.WebhookSecret} {
		if v == "" {
			errs = append(errs, fmt.Errorf("github.%s is required", key))
		}
	}
	if g.AppID != "" {
		if _, err := g.AppIDNumber(); err != nil {
			errs = append(errs, err)
		}
	}
	if g.PrivateKey != "" {
		if _, err := g.RSAPrivateKey(); err != nil {
			errs = append(errs, err)
		}
	}
	if g.APIURL != "" {
		u, err := url.Parse(g.APIURL)
		secure := err == nil && (u.Scheme == "https" || u.Scheme == "http" && isLoopback(u.Hostname()))
		if !secure || u.Host == "" {
			errs = append(errs, errors.New("github.api_url must be an absolute https URL (http only on a loopback host)"))
		}
	}
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errors.Join(errs...)
}

// isLoopback reports whether host names this machine: "localhost" or a
// loopback IP address.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AppIDNumber is AppID as the positive integer GitHub assigns.
func (g GitHub) AppIDNumber() (int64, error) {
	id, err := strconv.ParseInt(g.AppID, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("github.app_id must be a positive integer")
	}
	return id, nil
}

// RSAPrivateKey parses PrivateKey: an RSA key in PEM, PKCS #1 or PKCS #8.
// The error never quotes the key.
func (g GitHub) RSAPrivateKey() (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(g.PrivateKey))
	if block == nil {
		return nil, errors.New("github.private_key is not PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("github.private_key is not an RSA private key")
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github.private_key is not an RSA private key")
	}
	return rsaKey, nil
}

// BaseURL is APIURL with a trailing slash, or DefaultGitHubAPIURL.
func (g GitHub) BaseURL() string {
	if g.APIURL == "" {
		return DefaultGitHubAPIURL
	}
	return strings.TrimSuffix(g.APIURL, "/") + "/"
}

// ReviewQueue is how many pull requests may wait for a review worker in
// all: ReviewWorkers times ReviewQueuePerWorker. More webhooks for a pull
// request already waiting take no more room. It is an error when either is
// not positive or the total exceeds MaxReviewQueue. Load does not call
// it, so the CLI never fails on server settings; the review server does, at
// startup.
func (c *Config) ReviewQueue() (int, error) {
	if c.ReviewWorkers <= 0 || c.ReviewQueuePerWorker <= 0 {
		return 0, fmt.Errorf("review_workers and review_queue_per_worker must be positive, got %d and %d",
			c.ReviewWorkers, c.ReviewQueuePerWorker)
	}
	// Dividing rather than multiplying keeps huge values from overflowing.
	if c.ReviewQueuePerWorker > MaxReviewQueue/c.ReviewWorkers {
		return 0, fmt.Errorf("review_workers (%d) times review_queue_per_worker (%d) exceeds the review queue limit of %d",
			c.ReviewWorkers, c.ReviewQueuePerWorker, MaxReviewQueue)
	}
	return c.ReviewWorkers * c.ReviewQueuePerWorker, nil
}

// APITimeout is RequestTimeout, or DefaultGitHubRequestTimeout when it is
// not positive.
func (g GitHub) APITimeout() time.Duration {
	if g.RequestTimeout <= 0 {
		return DefaultGitHubRequestTimeout
	}
	return g.RequestTimeout
}

// SelectedPlatform is the platform the deployment reviews on: the one
// platform whose section has any key set (Enabled), identified from the
// configuration alone. That section must then be complete and valid, so a
// partly configured platform is an error rather than a server that starts
// and cannot authenticate. It is the zero Platform when no platform is
// configured, and an error when several are.
func (c *Config) SelectedPlatform() (Platform, error) {
	sections := map[Platform]interface {
		Enabled() bool
		Validate() error
	}{PlatformGitHub: c.GitHub}
	var on []Platform
	for name, s := range sections {
		if s.Enabled() {
			on = append(on, name)
		}
	}
	slices.Sort(on)
	switch len(on) {
	case 0:
		return "", nil
	case 1:
		if err := sections[on[0]].Validate(); err != nil {
			return "", fmt.Errorf("platform %s is partly configured: %w", on[0], err)
		}
		return on[0], nil
	}
	return "", fmt.Errorf("platforms %v are all configured; configure only one", on)
}

// Load resolves configuration from, in decreasing precedence: environment
// variables named with brand.EnvPrefix, a file passed explicitly (the CLI's
// --config), the file named by brand.EnvConfig, then brand.ConfigName in
// the current directory or $HOME. A missing config file is not an error:
// defaults and environment alone form a valid configuration. The standard
// logger is reconfigured to the requested verbosity before returning, so
// every slog.Default() call across the codebase already honors the verbose
// environment variable.
func Load(explicitPath string) (*Config, error) {
	v := viper.New()

	if explicitPath == "" {
		v.SetConfigName(brand.ConfigName)
		v.SetConfigType("yaml")
		if p := os.Getenv(brand.EnvConfig); p != "" {
			v.SetConfigFile(p)
		} else {
			v.AddConfigPath(".")
			if home, err := os.UserHomeDir(); err == nil {
				v.AddConfigPath(home)
			}
		}
	} else {
		v.SetConfigFile(explicitPath)
	}

	for _, key := range configKeys(reflect.TypeFor[Config](), "") {
		if err := v.BindEnv(key, envName(key)); err != nil {
			return nil, err
		}
	}

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		// An explicitly-specified --config path that resolves to nothing is a
		// user mistake: viper returns the raw open error there, and we must not
		// silently fall back to defaults (defaults would hide a typo). Only the
		// search-path case (brand.EnvConfig or the implicit home lookup) is
		// allowed to be empty and resolve to defaults+env.
		if !errors.As(err, &notFound) && explicitPath != "" {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		if !errors.As(err, &notFound) && os.Getenv(brand.EnvConfig) != "" {
			return nil, fmt.Errorf("read config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	// Defaults apply to unset and to non-positive values alike.
	if cfg.ServeAddr == "" {
		cfg.ServeAddr = DefaultServeAddr
	}
	if cfg.ReviewTimeout <= 0 {
		cfg.ReviewTimeout = DefaultReviewTimeout
	}
	if cfg.ReportTimeout <= 0 {
		cfg.ReportTimeout = DefaultReportTimeout
	}
	if cfg.ReviewWorkers <= 0 {
		cfg.ReviewWorkers = DefaultReviewWorkers
	}
	if cfg.ReviewQueuePerWorker <= 0 {
		cfg.ReviewQueuePerWorker = DefaultReviewQueuePerWorker
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = DefaultShutdownTimeout
	}
	if cfg.LinkLifetime <= 0 {
		cfg.LinkLifetime = DefaultLinkLifetime
	}

	slog.SetDefault(loggerFor(cfg.Verbose, os.Stderr))
	cfg.Path = v.ConfigFileUsed()
	return &cfg, nil
}

// SetVerbose reconfigures the standard logger to the level implied by v
// (debug when true, info when false). The CLI invokes it after Load when the
// -v flag was passed on the command line: an explicit flag always wins over
// the file and the environment.
func SetVerbose(v bool) {
	slog.SetDefault(loggerFor(v, os.Stderr))
}

// configKeys lists the dotted key of every configuration value in t, a
// struct whose fields carry mapstructure tags: nested structs are sections.
// Binding the environment from the struct keeps the two from drifting.
func configKeys(t reflect.Type, prefix string) []string {
	var keys []string
	for f := range t.Fields() {
		tag := f.Tag.Get("mapstructure")
		if tag == "" || tag == "-" {
			continue
		}
		if f.Type.Kind() == reflect.Struct {
			keys = append(keys, configKeys(f.Type, prefix+tag+".")...)
			continue
		}
		keys = append(keys, prefix+tag)
	}
	return keys
}

// envName is the environment variable for a dotted key: the brand prefix
// and the key in capitals, with sections joined by underscores
// ("github.app_id" -> "CANOPY_GITHUB_APP_ID").
func envName(key string) string {
	return brand.EnvPrefix + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// loggerFor builds the standard JSON structured logger for the process at the
// level implied by verbose (debug when true, info when false) writing to w.
// Load calls this against os.Stderr to set the default logger, and tests
// call it with an in-memory buffer to observe the output contract directly.
func loggerFor(verbose bool, w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}
