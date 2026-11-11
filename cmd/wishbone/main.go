package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"jsclayton.dev/wishbone/internal/backup"
	"jsclayton.dev/wishbone/internal/blocklist"
	"jsclayton.dev/wishbone/internal/clientip"
	"jsclayton.dev/wishbone/internal/config"
	"jsclayton.dev/wishbone/internal/gift"
	"jsclayton.dev/wishbone/internal/group"
	"jsclayton.dev/wishbone/internal/image"
	"jsclayton.dev/wishbone/internal/imagestore"
	"jsclayton.dev/wishbone/internal/item"
	"jsclayton.dev/wishbone/internal/list"
	"jsclayton.dev/wishbone/internal/logging"
	"jsclayton.dev/wishbone/internal/loginlink"
	"jsclayton.dev/wishbone/internal/occasion"
	"jsclayton.dev/wishbone/internal/passkey"
	"jsclayton.dev/wishbone/internal/server"
	"jsclayton.dev/wishbone/internal/session"
	"jsclayton.dev/wishbone/internal/setup"
	"jsclayton.dev/wishbone/internal/store"
	"jsclayton.dev/wishbone/internal/telemetry"
	"jsclayton.dev/wishbone/internal/user"
)

// Set at build time via -ldflags.
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

type CLI struct {
	Serve       ServeCmd       `cmd:"" help:"Start the web server."`
	Users       UsersCmd       `cmd:"" help:"List the users: ID, name, last name, and admin or deactivated."`
	SetAdmin    SetAdminCmd    `cmd:"" name:"set-admin" help:"Make a user the admin. The old admin keeps their sessions."`
	DeleteUser  DeleteUserCmd  `cmd:"" name:"delete-user" help:"Delete a user for good. It can't be undone. Refuses the admin."`
	LoginLink   LoginLinkCmd   `cmd:"" name:"login-link" help:"Print a login link for a user. It works once and expires after 7 days."`
	Backup      BackupCmd      `cmd:"" help:"Write a snapshot of the database and images to a new directory."`
	Healthcheck HealthcheckCmd `cmd:"" help:"Exit 0 if the local server is healthy. For container health checks."`
	Version     VersionCmd     `cmd:"" help:"Show version information."`
}

// BackupCmd snapshots a live data dir. It needs only the data dir, so it
// doesn't embed the full server config.
type BackupCmd struct {
	DataDir string `name:"data-dir" short:"d" env:"WISHBONE_DATA_DIR" default:"./data" help:"Directory for the SQLite database and images."`
	Path    string `arg:"" help:"New directory for the snapshot. It must not exist."`

	out io.Writer // stdout unless a test sets it
}

func (b *BackupCmd) Run() error {
	dbPath := filepath.Join(b.DataDir, "wishbone.db")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("no database at %s: %w", dbPath, err)
	}
	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	res, err := backup.Run(context.Background(), st.DB(), b.DataDir, b.Path)
	if err != nil && !errors.Is(err, backup.ErrMissingImages) {
		return err
	}
	missing := err
	out := b.out
	if out == nil {
		out = os.Stdout
	}
	if _, err := fmt.Fprintf(out, "Wrote database to %s\nCopied %d images to %s\n", res.Database, res.Images, res.ImageDir); err != nil {
		return err
	}
	// The snapshot is kept, but the exit code tells cron it is incomplete.
	return missing
}

// HealthcheckCmd probes the local server. The image is distroless, so it has no
// shell or curl for a container health check.
type HealthcheckCmd struct {
	Port int    `name:"port" short:"p" env:"WISHBONE_PORT" default:"8080" help:"HTTP port of the local server."`
	URL  string `name:"url" hidden:"" help:"Base URL to probe instead of localhost."`
}

func (h *HealthcheckCmd) Run() error {
	base := h.URL
	if base == "" {
		base = fmt.Sprintf("http://127.0.0.1:%d", h.Port)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v2/health", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", res.StatusCode)
	}
	return nil
}

// revealInterval is how often the server looks for gifts to give.
const revealInterval = 5 * time.Minute

type ServeCmd struct {
	config.Config `embed:""`
}

func (s *ServeCmd) Run() error {
	cfg := s.Config
	if err := cfg.Validate(); err != nil {
		return err
	}

	logger := logging.New(cfg.LogLevel, cfg.LogFormat,
		slog.String("app", "wishbone"),
		slog.String("version", version),
		slog.String("commit", commit),
	)

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	// Open runs the version guard: it refuses a database that a newer release
	// migrated, before any snapshot or migration.
	st, err := store.Open(context.Background(), filepath.Join(cfg.DataDir, "wishbone.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := migrate(ctx, st, cfg.DataDir, time.Now, logger); err != nil {
		return err
	}
	setupSvc := setup.NewService(st.DB(), time.Now)

	if err := announceSetup(ctx, setupSvc, logger, cfg.PublicURL); err != nil {
		return fmt.Errorf("setup code: %w", err)
	}

	// Tracing is off, with no exporter, unless an OTLP endpoint is set.
	tracer, shutdownTracing, err := telemetry.Setup(ctx, os.Getenv, version, logger, telemetry.OTLPHTTP)
	if err != nil {
		return fmt.Errorf("tracing: %w", err)
	}
	// Flush the last spans on every way out. This defer runs after the HTTP
	// server shuts down, because that happens before run returns.
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(flushCtx); err != nil {
			logger.Error("flush traces failed", slog.Any("error", err))
		}
	}()
	if tracer != nil {
		logger.Info("tracing enabled")
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	ips, err := clientip.New(cfg.TrustedProxies)
	if err != nil {
		return fmt.Errorf("trusted proxies: %w", err)
	}
	passkeys, err := passkey.New(st.DB(), time.Now, cfg.PublicURL)
	if err != nil {
		return fmt.Errorf("passkeys: %w", err)
	}
	// Only the e2e build can turn this on, so the browser tests can point link
	// reads and image downloads at a fixture page on this host.
	allowPrivate := allowPrivateAddrs(logger)
	extractor, err := newExtractor(cfg, st, logger, reg, allowPrivate, tracer)
	if err != nil {
		return fmt.Errorf("extraction: %w", err)
	}
	files, err := imagestore.New(filepath.Join(cfg.DataDir, "images"), imagestore.Options{AllowPrivateAddrs: allowPrivate, Tracer: tracer})
	if err != nil {
		return fmt.Errorf("images: %w", err)
	}
	images := image.NewService(st.DB(), files, time.Now)
	// The sweep deletes images that nothing holds. It runs until the server stops.
	go images.Schedule(ctx, logger)
	users := user.NewService(st.DB(), time.Now)
	lists := list.NewService(st.DB(), time.Now)
	items := item.NewService(st.DB(), time.Now)
	// A user with no timezone is in the server's zone (TZ).
	occasions := occasion.NewService(st.DB(), time.Now, occasion.WithFallbackZone(time.Local))
	gifts := gift.NewService(st.DB(), occasions, time.Now)
	// The gifts follow the changes that make an open gift wrong.
	groups := group.NewService(st.DB(), time.Now)
	gifts.Watch(lists, occasions, users, items, groups)
	// Reveal gives the gifts whose moment has come. The first run, at start, is the
	// catch-up after downtime.
	revealer := gift.NewRevealer(st.DB(), images, occasions, time.Now)
	go revealer.Schedule(ctx, revealInterval, logger)
	srv := server.New(server.Options{
		Logger:          logger,
		Store:           st,
		Version:         version,
		Commit:          commit,
		Tracer:          tracer,
		Metrics:         promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		Users:           users,
		Groups:          groups,
		Blocked:         blocklist.NewService(st.DB(), time.Now),
		Lists:           lists,
		Items:           items,
		Occasions:       occasions,
		Gifts:           gifts,
		Sessions:        session.NewService(st.DB(), time.Now),
		Setup:           setupSvc,
		LoginLinks:      loginlink.NewService(st.DB(), time.Now),
		Passkeys:        passkeys,
		Images:          images,
		Search:          extractor,
		PublicURL:       cfg.PublicURL,
		ClientIP:        ips,
		RateLimitRedeem: cfg.RateLimitRedeem,
	})

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           withE2EHooks(srv, revealer, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", slog.Int("port", cfg.Port), slog.String("public_url", cfg.PublicURL))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}

type VersionCmd struct {
	Verbose bool `short:"v" help:"Show detailed build information."`
}

func (v *VersionCmd) Run() error {
	if !v.Verbose {
		fmt.Printf("wishbone %s (%s)\n", version, commit)
		return nil
	}
	fmt.Printf("wishbone\n")
	fmt.Printf("  Version:    %s\n", version)
	fmt.Printf("  Commit:     %s\n", commit)
	fmt.Printf("  Build Time: %s\n", buildTime)
	fmt.Printf("  Go Version: %s\n", runtime.Version())
	fmt.Printf("  OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return nil
}

func main() {
	// A .env file is optional; a deployment sets real environment variables.
	_ = godotenv.Load()

	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("wishbone"),
		kong.Description("Private, self-hosted family wish lists."),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
	)
	ctx.FatalIfErrorf(ctx.Run())
}
