// Command kitweb is the review server: it receives a hosted platform's
// webhooks and reviews each change, reporting the outcome back on it. It is
// a separate binary from the kit CLI. The platform is deployment
// configuration (see config.Config.SelectedPlatform), chosen once at
// startup; the server itself is platform-agnostic.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/alphaeron/kit/internal/brand"
	"github.com/alphaeron/kit/internal/config"
	"github.com/alphaeron/kit/internal/platform/ghplatform"
	"github.com/alphaeron/kit/internal/server"
)

// platforms builds each hosted platform the server can run, by name. A new
// platform adds its constructor here and nothing else.
var platforms = map[config.Platform]func(cfg *config.Config, log *slog.Logger) (server.PRWebhook, error){
	config.PlatformGitHub: func(cfg *config.Config, log *slog.Logger) (server.PRWebhook, error) {
		return ghplatform.NewPRWebhook(cfg.GitHub, log)
	},
}

func main() {
	if err := run(); err != nil {
		slog.Error("kitweb stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "config file (default is $"+brand.EnvConfig+" or $HOME/"+brand.ConfigName+".yaml)")
	addr := flag.String("addr", "", "listen address; overrides serve_addr and "+brand.EnvPrefix+"SERVE_ADDR")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.ServeAddr = *addr
	}
	log := slog.Default()
	if cfg.Path != "" {
		log.Info("using config file", "component", "config", "path", cfg.Path)
	}

	queue, err := cfg.ReviewQueue()
	if err != nil {
		return err
	}
	name, err := cfg.SelectedPlatform()
	if err != nil {
		return err
	}
	newPlatform, ok := platforms[name]
	if !ok {
		return errors.New("no platform configured: set one platform's settings (see the README's Configuration section)")
	}
	platform, err := newPlatform(cfg, log)
	if err != nil {
		return fmt.Errorf("platform %s: %w", name, err)
	}
	log.Info("platform selected", "component", "server", "platform", name)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv, err := server.New(ctx, server.Options{
		Addr:            cfg.ServeAddr,
		Workers:         cfg.ReviewWorkers,
		Queue:           queue,
		ReviewTimeout:   cfg.ReviewTimeout,
		ReportTimeout:   cfg.ReportTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Version:         version(),
		Log:             log,
	}, platform)
	if err != nil {
		return err
	}
	return srv.ListenAndServe(ctx)
}

// version is the module version the binary was built from, or "dev".
func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
