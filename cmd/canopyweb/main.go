// Command canopyweb is the review server: it receives a hosted platform's
// webhooks and reviews each change, reporting the outcome back on it. It is
// a separate binary from the canopy CLI. The platform is deployment
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
	"sync"
	"syscall"
	"time"

	"github.com/alphaeron/canopy/internal/brand"
	"github.com/alphaeron/canopy/internal/config"
	"github.com/alphaeron/canopy/internal/platform/ghplatform"
	"github.com/alphaeron/canopy/internal/server"
	"github.com/alphaeron/canopy/internal/store"
)

// platforms builds each hosted platform the server can run, by name. A new
// platform adds its constructor here and nothing else.
var platforms = map[config.Platform]func(cfg *config.Config, repo *server.RepoCache, log *slog.Logger) (server.PRWebhook, error){
	config.PlatformGitHub: func(cfg *config.Config, repo *server.RepoCache, log *slog.Logger) (server.PRWebhook, error) {
		return ghplatform.NewPRWebhook(cfg.GitHub, repo, log)
	},
}

// pruneInterval is how often the server prunes its database.
const pruneInterval = time.Hour

func main() {
	if err := run(); err != nil {
		slog.Error("canopyweb stopped", "error", err)
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Interfaces stay nil without a database: a nil *store.Store in them
	// would not be.
	var (
		repoStore server.RepoStore
		recorder  server.Recorder
		history   server.ReviewHistory
	)
	if cfg.DatabaseURL != "" {
		db, err := store.Open(ctx, store.Options{
			URL:      cfg.DatabaseURL,
			Key:      []byte(cfg.DatabaseKey),
			MinConns: int32(min(cfg.ReviewWorkers, 1<<20)) + 8,
		})
		if err != nil {
			return err
		}
		defer db.Close()
		if err := db.ApplySchema(ctx); err != nil {
			return err
		}
		repoStore, recorder, history = db, db, db
		log.Info("database ready", "component", "store")

		// Stopped and waited for before the database closes, also when
		// run returns early.
		pruneCtx, stopPruning := context.WithCancel(ctx)
		var pruning sync.WaitGroup
		pruning.Go(func() { db.PruneEvery(pruneCtx, pruneInterval, log) })
		defer func() {
			stopPruning()
			pruning.Wait()
		}()
	}
	cache := server.NewRepoCache(repoStore, log)
	defer cache.Wait() // before the database closes
	platform, err := newPlatform(cfg, cache, log)
	if err != nil {
		return fmt.Errorf("platform %s: %w", name, err)
	}
	log.Info("platform selected", "component", "server", "platform", name)

	srv, err := server.New(ctx, server.Options{
		Addr:            cfg.ServeAddr,
		Workers:         cfg.ReviewWorkers,
		Queue:           queue,
		ReviewTimeout:   cfg.ReviewTimeout,
		ReportTimeout:   cfg.ReportTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Version:         version(),
		Log:             log,
		Recorder:        recorder,
		History:         history,
		PublicURL:       cfg.PublicURL,
		LinkKey:         []byte(cfg.LinkKey),
		LinkLifetime:    cfg.LinkLifetime,
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
