// Package config is the single place that discovers and parses the shared
// configuration for every binary (the CLI and REST API).
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
	"net/url"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/alphaeron/kit/internal/brand"
)

// Config is the fully resolved configuration for one process. Fields are
// plain values read from the configuration file and the prefixed
// environment variables, where environment wins over file and file wins over
// defaults. Each hosted platform has its own section, so adding a platform
// adds a section rather than top-level keys. Every key is documented in the
// "Environment variables" README section so the same table serves
// deployment of the REST API.
type Config struct {
	// Verbose selects debug-level structured logging; false selects info.
	Verbose bool `mapstructure:"verbose"`
	// ServeAddr is the REST API listen address; defaults to ":8080".
	ServeAddr string `mapstructure:"serve_addr"`
	// ReviewTimeout bounds one webhook-triggered review; defaults to 50
	// seconds.
	ReviewTimeout time.Duration `mapstructure:"review_timeout"`
	// ReviewWorkers is how many reviews the REST API runs at once;
	// defaults to 4.
	ReviewWorkers int `mapstructure:"review_workers"`
	// GitHub configures the GitHub platform for the REST API.
	GitHub GitHub `mapstructure:"github"`
	// Path is the file the configuration was read from, when one existed.
	// It is empty when defaults and environment alone formed the active
	// configuration. Callers log Path exactly once, replacing the old
	// "dump every key" behavior.
	Path string `mapstructure:"-"`
}

// GitHub is the GitHub platform's section: the GitHub App the REST API
// acts as. The CLI never reads it.
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
	// DefaultGitHubAPIURL.
	APIURL string `mapstructure:"api_url"`
}

// DefaultGitHubAPIURL is github.com's REST API.
const DefaultGitHubAPIURL = "https://api.github.com/"

// Defaults for the review server's settings.
const (
	DefaultServeAddr     = ":8080"
	DefaultReviewTimeout = 50 * time.Second
	DefaultReviewWorkers = 4
)

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
// is an absolute http(s) URL. Errors name keys, never secret values. Load
// does not call it, so the CLI never fails on server settings; the review
// server does, at startup.
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
		if u, err := url.Parse(g.APIURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs = append(errs, errors.New("github.api_url must be an absolute http(s) URL"))
		}
	}
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errors.Join(errs...)
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

	v.SetDefault("serve_addr", DefaultServeAddr)
	v.SetDefault("review_timeout", DefaultReviewTimeout)
	v.SetDefault("review_workers", DefaultReviewWorkers)
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
	if cfg.ServeAddr == "" {
		cfg.ServeAddr = DefaultServeAddr
	}
	if cfg.ReviewTimeout <= 0 {
		cfg.ReviewTimeout = DefaultReviewTimeout
	}
	if cfg.ReviewWorkers <= 0 {
		cfg.ReviewWorkers = DefaultReviewWorkers
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
// ("github.app_id" -> "KIT_GITHUB_APP_ID").
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
