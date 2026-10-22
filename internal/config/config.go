// Package config is the single place that discovers and parses the shared
// configuration for every binary (the CLI and REST API).
// Both entry points call Load first thing in main() and pass the resulting
// *Config by value into the internal packages; no other package owns a
// package-level viper instance. The only documented side effect of Load is
// slog.SetDefault(), so every existing internal-package slog call picks up
// the configured verbosity without knowing about configuration at all.
package config

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"

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
}

// legacyKey is a deprecated key and the key that replaced it.
type legacyKey struct{ old, key string }

// legacyKeys are the deprecated flat keys. A legacy key still configures its
// replacement when the replacement is not set itself, with a deprecation
// warning.
var legacyKeys = []legacyKey{
	{old: "github_app_id", key: "github.app_id"},
	{old: "github_private_key", key: "github.private_key"},
	{old: "webhook_hmac_secret", key: "github.webhook_secret"},
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

	v.SetDefault("serve_addr", ":8080")
	keys := configKeys(reflect.TypeFor[Config](), "")
	for _, l := range legacyKeys {
		// A legacy key whose variable is also its replacement's (as
		// KIT_GITHUB_APP_ID is) needs no binding of its own, and binding it
		// would warn about a variable that is not deprecated.
		if envName(l.old) != envName(l.key) {
			keys = append(keys, l.old)
		}
	}
	for _, key := range keys {
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

	var deprecated []legacyKey
	for _, l := range legacyKeys {
		if v.IsSet(l.old) {
			deprecated = append(deprecated, l)
			if !v.IsSet(l.key) {
				v.Set(l.key, v.Get(l.old))
			}
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if cfg.ServeAddr == "" {
		cfg.ServeAddr = ":8080"
	}

	slog.SetDefault(loggerFor(cfg.Verbose, os.Stderr))
	for _, l := range deprecated {
		slog.Warn("deprecated configuration key; use its replacement",
			"component", "config", "key", l.old, "replacement", l.key)
	}
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

// loggerFor builds the standard structured logger for the process at the
// level implied by verbose (debug when true, info when false) writing to w.
// Load calls this against os.Stderr to set the default logger, and tests
// call it with an in-memory buffer to observe the output contract directly.
func loggerFor(verbose bool, w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
