// Package main is the Orenda CLI entry point.
//
// Available subcommands:
//
//	orenda serve [--config FILE]      start HTTP server
//	orenda version                    print version and exit
//	orenda migrate up                 apply all pending migrations
//	orenda migrate down [--steps N]   rollback the last N migrations (Phase 1+)
//	orenda migrate status             list applied migrations
//	orenda backup push|status|snapshot    backup operations (stubs in Phase 0)
//
// Global flags:
//
//	--config FILE, -c FILE            path to config.yaml (default data/config.yaml)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/bot"
	"github.com/ramgml/orenda/internal/config"
	activityservice "github.com/ramgml/orenda/internal/service/activity"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	attachmentsvc "github.com/ramgml/orenda/internal/service/attachment"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	eventservice "github.com/ramgml/orenda/internal/service/event"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
	searchservice "github.com/ramgml/orenda/internal/service/search"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	timeentryservice "github.com/ramgml/orenda/internal/service/timeentry"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"

	activitydomain "github.com/ramgml/orenda/internal/domain/activity"
	attachmentdomain "github.com/ramgml/orenda/internal/domain/attachment"
	commentdomain "github.com/ramgml/orenda/internal/domain/comment"
)

// apiAttachmentResult aliases api.AttachmentResult so the adapter below
// doesn't need to import the api package twice.
type apiAttachmentResult = api.AttachmentResult
type apiAttachment = api.AttachmentService

// tokenMinterFor adapts the concrete sqlite.APITokenRepo to the agent
// service's TokenMinter interface by projecting the StoredToken row
// to (id, name, err).
func tokenMinterFor(repo *sqlite.APITokenRepo) agentservice.TokenMinter {
	return sqliteTokenMinterAdapter{repo: repo}
}

type sqliteTokenMinterAdapter struct {
	repo *sqlite.APITokenRepo
}

func (a sqliteTokenMinterAdapter) MintToken(ctx context.Context, userID, name, hash, scopesJSON string, expiresAt *time.Time) (string, string, error) {
	row, err := a.repo.Create(ctx, userID, name, hash, scopesJSON, expiresAt)
	if err != nil {
		return "", "", err
	}
	return row.ID, row.Name, nil
}

// commentAdderAdapter bridges comment.Service.Add (which returns
// *comment.Comment) to the task service's CommentAdder.Add (which
// returns string, error).
type commentAdderAdapter struct {
	svc *commentservice.Service
}

func (a commentAdderAdapter) Add(ctx context.Context, in *taskservice.CommentInput) (string, error) {
	c := &commentdomain.Comment{
		TargetType: commentdomain.TargetType(in.TargetType),
		TargetID:   in.TargetID,
		AuthorType: commentdomain.AuthorType(in.AuthorType),
		AuthorID:   in.AuthorID,
		BodyMD:     in.BodyMD,
	}
	got, err := a.svc.Add(ctx, c)
	if err != nil {
		return "", err
	}
	return got.ID, nil
}

func commentAdderFor(svc *commentservice.Service) taskservice.CommentAdder {
	return commentAdderAdapter{svc: svc}
}

// taskRecorderAdapter bridges activity.Recorder.RecordTask to
// taskSvc.Recorder.Record (different method names).
type taskRecorderAdapter struct{ inner *activityservice.Recorder }

func (a taskRecorderAdapter) Record(ctx context.Context, taskID string, actorType activitydomain.ActorType, actorID string, action activitydomain.Action, payload string) error {
	return a.inner.RecordTask(ctx, taskID, actorType, actorID, action, payload)
}

func taskRecorderFor(r *activityservice.Recorder) taskservice.Recorder {
	return taskRecorderAdapter{inner: r}
}

// attachmentAdapter bridges attachment.Service (returns *attachment.StoreResult)
// to api.AttachmentService (returns *api.AttachmentResult).
type attachmentAdapter struct{ inner *attachmentsvc.Service }

func (a attachmentAdapter) StoreFromBytes(
	ctx context.Context,
	t attachmentdomain.TargetType,
	targetID, filename, mime string,
	uploaderType attachmentdomain.UploaderType,
	uploaderID string,
	body io.Reader,
) (*apiAttachmentResult, error) {
	res, err := a.inner.StoreFromBytes(ctx, t, targetID, filename, mime, uploaderType, uploaderID, body)
	if err != nil {
		return nil, err
	}
	return &apiAttachmentResult{Attachment: res.Attachment, Duplicate: res.Duplicate}, nil
}

func (a attachmentAdapter) Get(ctx context.Context, id string) (*attachmentdomain.Attachment, error) {
	return a.inner.Get(ctx, id)
}

func (a attachmentAdapter) ListByTarget(ctx context.Context, t attachmentdomain.TargetType, targetID string) ([]*attachmentdomain.Attachment, error) {
	return a.inner.ListByTarget(ctx, t, targetID)
}

func (a attachmentAdapter) Delete(ctx context.Context, id string) error {
	return a.inner.Delete(ctx, id)
}

func attachmentServiceFor(s *attachmentsvc.Service) apiAttachment {
	return attachmentAdapter{inner: s}
}

// version is set by -ldflags at build time.
var version = "0.1.0-dev"

// commit is set by -ldflags at build time (git describe --always).
var commit = "dev"

// buildDate is set by -ldflags at build time.
var buildDate = "unknown"

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// newRootCmd constructs the top-level cobra command tree.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "orenda",
		Short:         "Orenda — local-first productivity suite with first-class AI agents",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.PersistentFlags().StringP("config", "c", "data/config.yaml", "path to config file")

	root.AddCommand(newServeCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newMigrateCmd())
	root.AddCommand(newBackupCmd())
	root.AddCommand(newUserCmd())

	return root
}

// ----------------------------------------------------------------------------
// serve
// ----------------------------------------------------------------------------

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the Orenda HTTP server",
		RunE:  runServe,
	}
}

func runServe(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	logger, err := buildLogger(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	absCfg, _ := filepath.Abs(cfgPath)
	logger.Info("starting orenda",
		zap.String("version", version),
		zap.String("commit", commit),
		zap.String("config", absCfg),
		zap.String("addr", fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)),
	)

	// Open SQLite database.
	dbPath := cfg.ResolveDBPath(cwdOr(absCfg, "."))
	db, err := sqlite.Open(cmd.Context(), dbPath, sqlite.OpenConfig{
		WALMode:       cfg.Storage.WALMode,
		EnableForeign: cfg.Storage.EnableForeign,
		BusyTimeoutMs: cfg.Storage.BusyTimeoutMs,
	})
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer func() { _ = db.Close() }()

	logger.Info("sqlite opened", zap.String("path", dbPath))

	// Ensure migrations are up to date before serving traffic.
	if err := sqlite.Migrate(cmd.Context(), db, sqlite.MigrationsFS, "migrations"); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	logger.Info("migrations applied")

	// Build repositories.
	users := sqlite.NewUserRepository(db)
	projects := sqlite.NewProjectRepository(db)
	tasksRepo := sqlite.NewTaskRepository(db)
	tokens := sqlite.NewAPITokenRepository(db)

	// Build service layer (Phase 2: task_service.Move; Phase 3.6 adds
	// Claim/Release/Submit/Review — wired with locks repo, Recorder/Comments
	// land in 3.7/3.9).
	hub := ws.NewHub()
	taskLocks := sqlite.NewTaskLockRepository(db)
	commentSvc := commentservice.New(sqlite.NewCommentRepository(db), hub, nil)
	activityRepo := sqlite.NewActivityRepository(db)
	activityRecorder := activityservice.New(activityRepo)
	taskSvc := taskservice.New(tasksRepo, taskLocks, taskRecorderFor(activityRecorder), commentAdderFor(commentSvc), hub)

	// Agent service (Phase 3.5) — Register, Heartbeat, SweepOffline.
	// Wired but not yet exposed via handlers (3.11).
	agentSvc := agentservice.New(
		sqlite.NewAgentRepository(db),
		users,
		tokenMinterFor(tokens),
		hub,
		nil, // Recorder lands with 3.9
	)
	_ = agentSvc

	// Event + Time services (Phase 4).
	eventSvc := eventservice.New(sqlite.NewEventRepository(db), hub, nil)
	timeSvc := timeentryservice.New(sqlite.NewTimeEntryRepository(db), hub, nil)

	// Wiki + Search services (Phase 5).
	wikiSvc := wikiservice.New(sqlite.NewWikiRepository(db), hub)
	searchSvc := searchservice.New(sqlite.NewSearchRepository(db), hub)

	// Notifier (Phase 6): registry + console bot (always available) + WS
	// hub publish. External transports land in Phase 10.
	botRegistry := bot.NewRegistry()
	botRegistry.Register(bot.Console{Out: os.Stderr})
	notifierSvc := notifierservice.New(
		sqlite.NewNotificationRepository(db),
		sqlite.NewBotSubscriptionRepository(db),
		botRegistry,
		hub,
	)

	// Build the JWT signer. JWT secret is mandatory for Phase 1+ — refuse
	// to start without it so the operator doesn't discover the missing
	// config at first login.
	if cfg.Auth.JWTSecret == "" {
		return fmt.Errorf("auth: ORENDA_AUTH__JWT_SECRET (or auth.jwt_secret in config) is required for `serve`")
	}
	signer := auth.NewSigner(cfg.Auth.JWTSecret, cfg.Auth.JWTTTL, "orenda")

	// Build the router.
	api.Version = version
	router := api.NewRouter(api.Dependencies{
		Logger:       logger,
		Signer:       signer,
		Users:        users,
		Projects:     projects,
		Tasks:        tasksRepo,
		Tokens:       tokens,
		TaskService:  taskSvc,
		AgentService: agentSvc,
		Agents:       sqlite.NewAgentRepository(db),
		Comments:     commentSvc,
		Attachments: attachmentServiceFor(attachmentsvc.New(sqlite.NewAttachmentRepository(db), attachmentsvc.Config{
			UploadDir:    cfg.Uploads.Dir,
			MaxSizeBytes: int64(cfg.Uploads.MaxSizeMB) * 1024 * 1024,
			AllowedMimes: cfg.Uploads.AllowedMimes,
		}, hub)),
		Activities:    activityRepo,
		EventService:  eventSvc,
		TimeService:   timeSvc,
		WikiService:   wikiSvc,
		SearchService: searchSvc,
		Notifier:      notifierSvc,
		WSHub:         hub,
		CookieName:    cfg.Auth.CookieName,
	})

	// HTTP server with graceful shutdown.
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

// cwdOr returns the current working directory, falling back to fallback if
// os.Getwd() fails. It is used as the base for resolving relative paths from
// the config (db_path, mirror_dir, etc).
//
// absCfg is currently unused; the parameter is kept so future versions can
// resolve paths relative to the config file when needed (e.g. if the
// operator installs Orenda in /opt but runs from /home/me).
func cwdOr(_ string, fallback string) string {
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return fallback
	}
	return wd
}

// ----------------------------------------------------------------------------
// version
// ----------------------------------------------------------------------------

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("orenda %s (commit %s, built %s)\n", version, commit, buildDate)
		},
	}
}

// ----------------------------------------------------------------------------
// migrate
// ----------------------------------------------------------------------------

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Database migrations",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrate(cmd, migrateUp)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "down",
		Short: "Rollback the last migration (Phase 1+)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrate(cmd, migrateDown)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "List applied migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrate(cmd, migrateStatus)
		},
	})
	return cmd
}

type migrateAction int

const (
	migrateUp migrateAction = iota
	migrateDown
	migrateStatus
)

func runMigrate(cmd *cobra.Command, action migrateAction) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	logger, err := buildLogger(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Sync() }()

	db, cleanup, err := openCLIDB(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	switch action {
	case migrateUp:
		if err := sqlite.Migrate(cmd.Context(), db, sqlite.MigrationsFS, "migrations"); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		versions, _ := sqlite.AppliedVersions(cmd.Context(), db)
		logger.Info("migrate up complete", zap.Strings("applied", versions))
		fmt.Println("applied:", versions)
	case migrateDown:
		// Phase 1+ will track down-migrations explicitly.
		// For now we just record that the operator wanted a rollback.
		logger.Warn("migrate down is not yet implemented (Phase 1+); no changes made")
		fmt.Println("migrate down: not implemented (Phase 1+)")
	case migrateStatus:
		// Need a migrations table to query status; create it lazily.
		if _, err := db.ExecContext(cmd.Context(), `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
			return err
		}
		versions, err := sqlite.AppliedVersions(cmd.Context(), db)
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			fmt.Println("no migrations applied")
		} else {
			fmt.Println("applied migrations:")
			for _, v := range versions {
				fmt.Println(" -", v)
			}
		}
	}
	return nil
}

// ----------------------------------------------------------------------------
// backup
// ----------------------------------------------------------------------------

func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Backup operations (Phase 7; Phase 0 is a no-op stub)",
	}

	for _, sub := range []struct {
		use, short, help string
	}{
		{"push", "Commit and push mirror to git remote", "Not implemented in Phase 0; will land in Phase 7."},
		{"snapshot", "Create SQLite snapshot", "Not implemented in Phase 0; will land in Phase 7."},
		{"status", "Show backup status", "Not implemented in Phase 0; will land in Phase 7."},
	} {
		s := sub // capture
		cmd.AddCommand(&cobra.Command{
			Use:   s.use,
			Short: s.short,
			Long:  s.help,
			Run: func(_ *cobra.Command, _ []string) {
				fmt.Printf("orenda backup %s: not implemented (Phase 7)\n", s.use)
			},
		})
	}
	return cmd
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// buildLogger constructs a zap.Logger from the config section.
//
// Phase 0 logs only to stderr — file rotation (cfg.Logging.Path) lands with
// the structured-logging refactor in Phase 9.
func buildLogger(cfg *config.Config) (*zap.Logger, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(cfg.Logging.Level)); err != nil {
		level = zapcore.InfoLevel
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "ts"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	if cfg.Logging.Format == "console" {
		encoder = zapcore.NewConsoleEncoder(encCfg)
	} else {
		encoder = zapcore.NewJSONEncoder(encCfg)
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stderr), level)
	return zap.New(core, zap.AddCaller()), nil
}

// suppress unused-import warning on systems without time package usage.
var _ = time.Second
