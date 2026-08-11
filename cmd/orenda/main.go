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
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/backup"
	"github.com/ramgml/orenda/internal/bot"
	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/mirror"
	activityservice "github.com/ramgml/orenda/internal/service/activity"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	attachmentsvc "github.com/ramgml/orenda/internal/service/attachment"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	courseservice "github.com/ramgml/orenda/internal/service/course"
	eventservice "github.com/ramgml/orenda/internal/service/event"
	"github.com/ramgml/orenda/internal/service/notifier"
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

// ----------------------------------------------------------------------------
// Bot callback adapters (Phase 10)
// ----------------------------------------------------------------------------

// reviewDeciderAdapter bridges tasksvc.Service.Review to bot.ReviewDecider.
type reviewDeciderAdapter struct{ svc *taskservice.Service }

func (a reviewDeciderAdapter) Review(ctx context.Context, taskID, userID, decision, comment string) error {
	_, err := a.svc.Review(ctx, taskID, userID, taskservice.ReviewDecision(decision), comment)
	return err
}

// ownerResolverAdapter returns the single owner user (Phase 10 is
// single-owner; multi-user resolution lands with Phase 11).
type ownerResolverAdapter struct {
	users firstIDer
}

type firstIDer interface {
	FirstID(ctx context.Context) (string, error)
}

// ResolveOwner implements bot.UserResolver.
func (a ownerResolverAdapter) ResolveOwner(ctx context.Context, _ string) (string, error) {
	return a.users.FirstID(ctx)
}

// int64ToString is a tiny helper (no strconv needed elsewhere).
func int64ToString(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// findTelegramSubscriber returns the user_id whose Telegram
// subscription targets the given chat_id.
//
// The notifier.SubscriptionRepository already exposes
// ListByBotType for this — we adapt it to a small projection here
// so the wire-up code doesn't depend on the full notifier.Subscription
// shape (we only need four fields).

// findTelegramSubscriber returns the user_id whose Telegram
// subscription targets the given chat_id.
//
// The notifier.SubscriptionRepository already exposes
// ListByBotType for this. We operate on the notifier.Subscription
// shape directly because the bot side already imports it; the
// shape has TargetAddress + Enabled fields that are exactly what
// we need, and the projection is cheaper than a second type.
func findTelegramSubscriber(
	ctx context.Context,
	repo notifier.SubscriptionRepository,
	chatID int64,
) string {
	addr := int64ToString(chatID)
	subs, err := repo.ListByBotType(ctx, "telegram")
	if err != nil || len(subs) == 0 {
		return ""
	}
	for _, s := range subs {
		if !s.Enabled {
			continue
		}
		if s.TargetAddress == addr {
			return s.UserID
		}
	}
	return ""
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

func (a attachmentAdapter) ListByProject(ctx context.Context, projectID string) ([]*attachmentdomain.ProjectAttachment, error) {
	return a.inner.ListByProject(ctx, projectID)
}

func (a attachmentAdapter) Delete(ctx context.Context, id string) error {
	return a.inner.Delete(ctx, id)
}

func (a attachmentAdapter) Open(
	ctx context.Context,
	id string,
) (*attachmentdomain.Attachment, api.ReadSeekCloser, error) {
	return a.inner.Open(ctx, id)
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
	root.AddCommand(newAgentCmd())
	root.AddCommand(newMCPCmd())

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

	// Calendar events can be created with or without a project — the
	// event service no longer falls back to a system "Inbox" project,
	// it simply files events with project_id IS NULL when no project
	// is supplied. Migration 015 also drops the system Inbox project
	// and user, so there's nothing to bootstrap here.

	// Build repositories.
	users := sqlite.NewUserRepository(db)
	projects := sqlite.NewProjectRepository(db)
	tasksRepo := sqlite.NewTaskRepository(db)
	tokens := sqlite.NewAPITokenRepository(db)
	usersRaw := users // *userRepo for FirstID; api takes the domain interface

	// Backup service + scheduler (Phase 7) — constructed before the other
	// services so task/wiki services can hold a reference to the mirror.
	var backupSvc *backup.Service
	var mirrorSvc *mirror.Service
	if cfg.Backup.Enabled {
		if err := os.MkdirAll(cfg.Backup.MirrorDir, 0o755); err != nil {
			return fmt.Errorf("backup mirror dir: %w", err)
		}
		if err := os.MkdirAll(cfg.Backup.SnapshotDir, 0o755); err != nil {
			return fmt.Errorf("backup snapshot dir: %w", err)
		}
		mirrorSvc = mirror.New(cfg.Backup.MirrorDir)
		backupSvc = backup.New(backup.Config{
			MirrorDir:            cfg.Backup.MirrorDir,
			SnapshotDir:          cfg.Backup.SnapshotDir,
			DBPath:               cfg.ResolveDBPath("."),
			RemoteURL:            cfg.Backup.RemoteURL,
			RemoteAuth:           cfg.Backup.RemoteAuth,
			SnapshotRotationDays: cfg.Backup.SnapshotRotationDays,
		}, db)
		scheduler := backup.NewScheduler(backupSvc)
		go scheduler.Run(cmd.Context())
		logger.Info("backup scheduler started",
			zap.String("mirror_dir", cfg.Backup.MirrorDir),
			zap.String("snapshot_dir", cfg.Backup.SnapshotDir),
		)
	}

	// Build service layer (Phase 2: task_service.Move; Phase 3.6 adds
	// Claim/Release/Submit/Review — wired with locks repo, Recorder/Comments
	// land in 3.7/3.9).
	hub := ws.NewHub()
	taskLocks := sqlite.NewTaskLockRepository(db)
	commentSvc := commentservice.New(sqlite.NewCommentRepository(db), hub, nil)
	activityRepo := sqlite.NewActivityRepository(db)
	activityRecorder := activityservice.New(activityRepo)
	taskSvc := taskservice.New(tasksRepo, taskLocks, taskRecorderFor(activityRecorder), commentAdderFor(commentSvc), hub)
	taskSvc.Mirror = mirrorSvc
	taskSvc.Columns = projects // Phase 23.1 + 16.7: WIP lookup + inbox→project filing

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
	// Phase 11: events are stored as tasks with start_at/end_at. The
	// eventService facade still exists for API compatibility, but it
	// reads and writes the tasks table now.
	eventSvc := eventservice.New(sqlite.NewTaskRepository(db), hub, nil)
	timeSvc := timeentryservice.New(sqlite.NewTimeEntryRepository(db), hub, nil)

	// Wiki + Search services (Phase 5).
	wikiSvc := wikiservice.New(sqlite.NewWikiRepository(db), hub)
	wikiSvc.Mirror = mirrorSvc
	searchSvc := searchservice.New(sqlite.NewSearchRepository(db), hub)

	// Phase 18: courses (LMS).
	courseRepo := sqlite.NewCourseRepository(db)
	courseSvc := courseservice.New(courseRepo)

	// Notifier (Phase 6): registry + console bot (always available) + WS
	// hub publish. External transports land in Phase 10.
	botRegistry := bot.NewRegistry()
	botRegistry.Register(bot.Console{Out: os.Stderr})

	// Phase 10: config-driven bots.
	botSpecs := make([]bot.ConfigSpec, 0, len(cfg.Bots))
	for _, b := range cfg.Bots {
		botSpecs = append(botSpecs, bot.ConfigSpec{
			Type:    b.Type,
			Enabled: b.Enabled,
			Config:  b.Config,
		})
	}
	if err := bot.BuildFromConfig(botSpecs, botRegistry); err != nil {
		return fmt.Errorf("bots: %w", err)
	}
	// Start any bots that have long-running loops (telegram long-poll).
	for _, b := range botRegistry.List() {
		if err := b.Start(cmd.Context()); err != nil {
			logger.Warn("bot start failed", zap.String("name", b.Name()), zap.Error(err))
		}
	}

	// Phase 10: bot callback handler — converts approve/reject button presses
	// into task review decisions.
	var botCallback *bot.CallbackHandler
	{
		reviewDecider := reviewDeciderAdapter{svc: taskSvc}
		ownerResolver := ownerResolverAdapter{users: usersRaw}
		botCallback = bot.NewCallbackHandler(reviewDecider, ownerResolver)
		// Telegram: route callbacks through the bot's OnCallback hook.
		if tg, ok := botRegistry.Get("telegram").(*bot.Telegram); ok && tg != nil {
			tg.OnCallback = func(ctx context.Context, q bot.CallbackQuery) error {
				action, target, err := bot.ParseCallbackData(q.Data)
				if err != nil {
					return err
				}
				herr := botCallback.Handle(ctx, bot.CallbackAction{
					Action:    action,
					TaskID:    target,
					Nonce:     q.ID,
					BotUserID: int64ToString(q.ChatID),
				})
				if herr != nil {
					return herr
				}
				return tg.AnswerCallback(ctx, q.ID, "ok")
			}

			// Phase 21: route plain text messages from a private chat
			// into the Inbox. The simplest flow:
			//   1. Look up the user subscribed to this chat_id.
			//   2. Create an inbox task (project_id IS NULL) with the
			//      message text as title (truncated to 200 chars).
			//   3. Reply "✅ Captured to Inbox".
			// Subscription lookup is best-effort: no subscription =
			// ignore. The single-owner install has one user row, so
			// "the user subscribed to this telegram chat" is the
			// normal case after `orenda subscription add telegram …
			// target=<chat_id>`.
			tg.OnMessage = func(ctx context.Context, m bot.InboxMessage) error {
				subRepo := sqlite.NewBotSubscriptionRepository(db)
				addr := int64ToString(m.ChatID)
				subs, err := subRepo.ListByBotType(ctx, "telegram")
				var owner string
				if err == nil {
					for _, s := range subs {
						if s.Enabled && s.TargetAddress == addr {
							owner = s.UserID
							break
						}
					}
				}
				if owner == "" {
					return nil
				}
				title := strings.TrimSpace(m.Text)
				if len(title) > 200 {
					title = title[:200] + "…"
				}
				now := time.Now().UTC()
				tr := &task.Task{
					ProjectID:    "",
					Title:        title,
					Status:       task.StatusTodo,
					Priority:     task.PriorityMedium,
					Awaiting:     task.AwaitingNone,
					AssigneeType: task.AssigneeUser,
					AssigneeID:   owner,
					TimeSpentS:   0,
					Position:     0,
					AllDay:       false,
					CreatedAt:    now,
					UpdatedAt:    now,
				}
				_ = owner
				if err := tasksRepo.Create(ctx, tr); err != nil {
					return err
				}
				return tg.SendReply(ctx, m.ChatID, "✅ Captured to Inbox")
			}
		}
	}

	notifierSvc := notifierservice.New(
		sqlite.NewNotificationRepository(db),
		sqlite.NewBotSubscriptionRepository(db),
		botRegistry,
		hub,
	)

	// Phase 8 follow-up: recurring-event reminder scheduler.
	// Scans the [now+Lead, now+Lead+Window] band every Tick and fires
	// event.upcoming_1h notifications for the project owner. PRD F-C-4.
	reminder := &eventservice.Reminder{
		Repo:   sqlite.NewTaskRepository(db),
		Notify: notifierSvc.Notify,
		NotifyProjectOwner: func(ctx context.Context, eventID string) (ownerID, title, link string, err error) {
			ev, err := eventSvc.Get(ctx, eventID)
			if err != nil || ev == nil {
				return "", "", "", err
			}
			if ev.ProjectID == "" {
				return "", "", "", nil
			}
			p, err := projects.GetProject(ctx, ev.ProjectID)
			if err != nil || p == nil {
				return "", "", "", err
			}
			return p.OwnerID, ev.Title, "/calendar", nil
		},
	}

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
			UploadDir:    cfg.ResolveUploadsDir(cwdOr(absCfg, ".")),
			MaxSizeBytes: int64(cfg.Uploads.MaxSizeMB) * 1024 * 1024,
			AllowedMimes: cfg.Uploads.AllowedMimes,
		}, hub)),
		Activities:          activityRepo,
		EventService:        eventSvc,
		TimeService:         timeSvc,
		WikiService:         wikiSvc,
		SearchService:       searchSvc,
		Courses:             courseRepo,
		CourseService:       courseSvc,
		Notifier:            notifierSvc,
		Backup:              backupSvc,
		BackupEnabled:       cfg.Backup.Enabled,
		BackupRemoteURL:     cfg.Backup.RemoteURL,
		BackupRemoteAuthSet: cfg.Backup.RemoteAuth != "",
		SyncOps:             sqlite.NewSyncOpsRepository(db),
		BotCallback:         botCallback,
		WSHub:               hub,
		CookieName:          cfg.Auth.CookieName,
		DBPath:              cfg.ResolveDBPath("."),
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

	// Kick off the recurring-event reminder scheduler. It loops on
	// ctx.Done() and exits with the server.
	go reminder.Run(ctx)

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

// newBackupCmd is defined in cmd/orenda/backup.go.

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// buildLogger constructs a zap.Logger from the config section.
//
// Phase 9.4: writes JSON to stderr AND to cfg.Logging.Path with rotation
// (lumberjack). The stderr copy keeps `make dev` usable without a file;
// the file copy persists logs across restarts.
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

	cores := []zapcore.Core{
		zapcore.NewCore(encoder, zapcore.AddSync(os.Stderr), level),
	}

	if cfg.Logging.Path != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.Logging.Path), 0o755); err != nil {
			return nil, fmt.Errorf("logger: mkdir logs: %w", err)
		}
		rotator := &lumberjack.Logger{
			Filename:   cfg.Logging.Path,
			MaxSize:    32, // MiB per file
			MaxBackups: 5,
			MaxAge:     30, // days
			Compress:   true,
		}
		cores = append(cores,
			zapcore.NewCore(encoder, zapcore.AddSync(rotator), level))
	}

	return zap.New(zapcore.NewTee(cores...), zap.AddCaller()), nil
}

// suppress unused-import warning on systems without time package usage.
var _ = time.Second
