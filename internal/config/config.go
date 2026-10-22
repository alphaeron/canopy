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
	"strings"

	"github.com/spf13/viper"

	"github.com/alphaeron/kit/internal/brand"
)

// Config is the fully resolved configuration for one process. Fields are
// plain values read from the configuration file and the prefixed
// environment variables, where environment wins over file and file wins over
// defaults. Every key is documented in the "Environment variables" README
// section so the same table serves deployment of the REST API.
type Config struct {
	// Verbose selects debug-level structured logging; false selects info.
	Verbose bool `mapstructure:"verbose"`
	// GitHubAppID is the numeric GitHub App ID, kept as a string so the file
	// and environment supply exactly the same value with no numeric
	// coercion surprises.
	GitHubAppID string `mapstructure:"github_app_id"`
	// GitHubPrivateKey is the GitHub App RSA private key (PEM), used by the
	// REST API to mint installation tokens. Never logged.
	GitHubPrivateKey string `mapstructure:"github_private_key"`
	// WebhookHMACSecret is the GitHub webhook signing secret; the REST API
	// rejects signatures that do not verify against it.
	WebhookHMACSecret string `mapstructure:"webhook_hmac_secret"`
	// ServeAddr is the REST API listen address; defaults to ":8080".
	ServeAddr string `mapstructure:"serve_addr"`
	// Path is the file the configuration was read from, when one existed.
	// It is empty when defaults and environment alone formed the active
	// configuration. Callers log Path exactly once, replacing the old
	// "dump every key" behavior.
	Path string
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
	for _, key := range []string{"verbose", "github_app_id", "github_private_key", "webhook_hmac_secret", "serve_addr"} {
		if err := v.BindEnv(key, brand.EnvPrefix+upperEnvKey(key)); err != nil {
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
		cfg.ServeAddr = ":8080"
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

// upperEnvKey uppercases the environment prefix form of a dotted config key;
// the keys this package binds have no dotted form, so the result is an
// all-caps name (e.g. "github_app_id" -> "GITHUB_APP_ID").
func upperEnvKey(key string) string {
	return strings.ToUpper(key)
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
