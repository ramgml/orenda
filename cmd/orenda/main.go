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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof" // Phase 28.6: registers /debug/pprof/* on http.DefaultServeMux
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
	coursedomain "github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/mirror"
	activityservice "github.com/ramgml/orenda/internal/service/activity"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	attachmentsvc "github.com/ramgml/orenda/internal/service/attachment"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	courseservice "github.com/ramgml/orenda/internal/service/course"
	eventservice "github.com/ramgml/orenda/internal/service/event"
	"github.com/ramgml/orenda/internal/service/notifier"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
	projectservice "github.com/ramgml/orenda/internal/service/project"
	searchservice "github.com/ramgml/orenda/internal/service/search"
	studyservice "github.com/ramgml/orenda/internal/service/study"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	timeentryservice "github.com/ramgml/orenda/internal/service/timeentry"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"

	activitydomain "github.com/ramgml/orenda/internal/domain/activity"
	attachmentdomain "github.com/ramgml/orenda/internal/domain/attachment"
	commentdomain "github.com/ramgml/orenda/internal/domain/comment"
	taskdomain "github.com/ramgml/orenda/internal/domain/task"
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

// backupFailedAdapter bridges the notifier service to the backup
// scheduler's FailureNotifier seam. Phase Wave 4 PR 2: every
// background-job error fans out a `backup.failed` event with the
// op name (git_push / sqlite_snapshot / wal_checkpoint) as the dedup
// key suffix so a single stuck op doesn't spam the inbox.
type backupFailedAdapter struct{ svc *notifierservice.Service }

func (a backupFailedAdapter) NotifyBackupFailed(ctx context.Context, op string, err error) {
	if a.svc == nil || err == nil {
		return
	}
	_ = a.svc.Notify(ctx, notifierservice.Event{
		Type:       "backup.failed",
		TargetType: "backup",
		TargetID:   op,
		Title:      "Backup step failed: " + op,
		Body:       err.Error(),
		DedupKey:   "backup.failed:" + op,
	})
}

// commentNotifierAdapter bridges notifier.Service to the comment
// service's Notifier seam. Phase Wave 4 PR 2: every task-targeted
// comment fans out a `task.commented` event to the task owner.
type commentNotifierAdapter struct{ svc *notifierservice.Service }

func (a commentNotifierAdapter) Notify(ctx context.Context, e notifierservice.Event) error {
	if a.svc == nil {
		return nil
	}
	return a.svc.Notify(ctx, e)
}

// taskOwnerResolverAdapter maps a task_id to (owner_user_id,
// title) for the comment service's TaskOwnerResolver seam.
// The lookup walks the task → project → owner chain, falling
// back to the first non-system user (mirrors the existing
// firstNonSystemUserID helper used by notifyTaskAssignee).
type taskOwnerResolverAdapter struct {
	tasks    task.Repository
	projects project.Repository
	users    user.Repository
}

func (a taskOwnerResolverAdapter) OwnerForTask(ctx context.Context, taskID string) (string, string, error) {
	if a.tasks == nil || taskID == "" {
		return "", "", nil
	}
	tr, err := a.tasks.GetByID(ctx, taskID)
	if err != nil || tr == nil {
		return "", "", nil
	}
	// Inbox tasks (project_id empty) have no recipient — Phase 16.
	if tr.ProjectID == "" {
		return "", "", nil
	}
	if a.projects == nil {
		return "", "", nil
	}
	p, err := a.projects.GetProject(ctx, tr.ProjectID)
	if err != nil || p == nil {
		return "", "", nil
	}
	return p.OwnerID, tr.Title, nil
}

// pendingNotifier holds a deferred wiring step (the backup
// scheduler wants a Notifier but the notifier service hasn't been
// constructed yet at the point we build the scheduler). It gets
// filled in once notifierSvc is ready, then used to call
// WithNotifier on the scheduler.
var pendingNotifier *backup.Scheduler

// courseTaskCreatorAdapter implements coursesvc.TaskCreator by
// writing directly to the task repository. The course service only
// needs to create rows; the rest of the task pipeline (WS events,
// activity log) is intentionally not wired here — those would
// duplicate the createTask handler. The generator task is a
// stand-alone "look at this course" marker; the review task is a
// stand-alone "score this answer" marker. Both are claimable
// through the regular agent flow.
//
// Phase 27.4: the course service stays free of the task service
// dependency graph. The adapter lives in main.go where the
// dependency wiring already lives.
//
// Phase 27.9: the previous design intentionally skipped WS/activity
// hooks here so it wouldn't duplicate the createTask handler. That
// decision left the UI blind to course-spawned tasks (no live
// notification, no review-queue badge). The fix is bounded: the
// adapter now publishes a single `task.created` event and writes a
// single activity row per generated task. Both go through the same
// Hub / Recorder already wired in main.go so live subscribers see
// the task immediately.
type courseTaskCreatorAdapter struct {
	tasksRepo taskdomain.Repository
	db        *sql.DB
	hub       ws.Hub
	recorder  courseTaskActivityRecorder
}

// courseTaskActivityRecorder is the narrow surface the adapter needs
// to write the "task created" activity row. We don't import the
// task service's Recorder directly to keep this adapter wiring
// isolated from the task service's full dependency graph. The
// concrete implementation (activitysvc.Recorder) satisfies it via
// RecordTask.
type courseTaskActivityRecorder interface {
	RecordTask(ctx context.Context, taskID string, actorType activitydomain.ActorType, actorID string, action activitydomain.Action, payload string) error
}

func (a courseTaskCreatorAdapter) CreateGeneratorTask(ctx context.Context, ownerID, courseID, title, intentMD string) (string, error) {
	t := &taskdomain.Task{
		Title:     fmt.Sprintf("Build curriculum for: %s", title),
		ContextMD: fmt.Sprintf("course_id=%s\nintent=%s\n", courseID, intentMD),
		Status:    taskdomain.StatusTodo,
		Priority:  taskdomain.PriorityMedium,
		Awaiting:  taskdomain.AwaitingAgent,
		// ProjectID left empty — Phase 16 inbox cards float until
		// the agent (or the user) files them under a project.
		AssigneeType: taskdomain.AssigneeType("user"),
		AssigneeID:   ownerID,
	}
	if err := a.tasksRepo.Create(ctx, t); err != nil {
		return "", err
	}
	a.notifyCreated(ctx, t, ownerID, "course_generator")
	return t.ID, nil
}

func (a courseTaskCreatorAdapter) CreateQuizReviewTask(ctx context.Context, ownerID, quizID, lessonID, answer string) (string, error) {
	// Look up the quiz + lesson for a useful title — the tutor
	// agent needs to know which lesson they're reviewing without
	// a separate API call.
	quizTitle, lessonTitle, err := a.lookupQuizContext(ctx, quizID, lessonID)
	if err != nil {
		return "", err
	}
	t := &taskdomain.Task{
		Title:        fmt.Sprintf("Review quiz answer: %s / %s", lessonTitle, quizTitle),
		ContextMD:    fmt.Sprintf("quiz_id=%s\nlesson_id=%s\nanswer=%s\n", quizID, lessonID, answer),
		Status:       taskdomain.StatusTodo,
		Priority:     taskdomain.PriorityMedium,
		Awaiting:     taskdomain.AwaitingAgent,
		AssigneeType: taskdomain.AssigneeType("user"),
		AssigneeID:   ownerID,
	}
	if err := a.tasksRepo.Create(ctx, t); err != nil {
		return "", err
	}
	a.notifyCreated(ctx, t, ownerID, "course_quiz_review")
	return t.ID, nil
}

// notifyCreated publishes the WS `task.created` event and writes a
// matching activity row for tasks spawned by the course service
// (Phase 27.9). Errors are swallowed on purpose — best-effort
// observability for a row that already lives in the DB; surfacing
// would risk failing a successful course operation.
func (a courseTaskCreatorAdapter) notifyCreated(ctx context.Context, t *taskdomain.Task, ownerID, source string) {
	if a.hub != nil {
		a.hub.Publish(ctx, ws.Event{
			Topic: "tasks",
			Body: map[string]any{
				"type":    "task.created",
				"task_id": t.ID,
				"user_id": ownerID,
				"source":  source,
				"task":    t,
			},
		})
	}
	if a.recorder != nil {
		_ = a.recorder.RecordTask(ctx, t.ID, activitydomain.ActorSystem, ownerID,
			activitydomain.ActionCreated,
			fmt.Sprintf(`{"source":%q}`, source))
	}
}

func (a courseTaskCreatorAdapter) lookupQuizContext(ctx context.Context, quizID, lessonID string) (quizTitle, lessonTitle string, err error) {
	const q = `SELECT q.question_md, l.title FROM course_quizzes q
		JOIN course_lessons l ON l.id = q.lesson_id
		WHERE q.id = ? AND l.id = ?`
	row := a.db.QueryRowContext(ctx, q, quizID, lessonID)
	if err := row.Scan(&quizTitle, &lessonTitle); err != nil {
		if err == sql.ErrNoRows {
			return "", "", fmt.Errorf("course: quiz or lesson not found")
		}
		return "", "", err
	}
	return quizTitle, lessonTitle, nil
}

// CompleteTask retires a course-related task (Phase 27.6 generator
// seam). We load the task, flip it to status=done with the current
// timestamp, clear the awaiting flag, and append the note to the
// existing context_md so a future audit can see why it ended.
//
// This path intentionally does not go through the task service /
// activity log: the course service is upstream of both, and the
// task is being retired by the course's own state machine. Keeping
// it isolated here means a missing task service in a future
// minimal configuration can't take down course submission.
func (a courseTaskCreatorAdapter) CompleteTask(ctx context.Context, taskID, note string) error {
	if taskID == "" {
		return nil
	}
	t, err := a.tasksRepo.GetByID(ctx, taskID)
	if err != nil {
		// Already gone or never existed — treat as best-effort
		// success so the course swap can commit.
		if errors.Is(err, taskdomain.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("course.CompleteTask: load: %w", err)
	}
	now := time.Now().UTC()
	t.Status = taskdomain.StatusDone
	t.Awaiting = taskdomain.AwaitingNone
	t.CompletedAt = &now
	if note != "" {
		if t.ContextMD != "" {
			t.ContextMD += "\n"
		}
		t.ContextMD += "course_retired: " + note
	}
	if err := a.tasksRepo.Update(ctx, t); err != nil {
		return fmt.Errorf("course.CompleteTask: write: %w", err)
	}
	return nil
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

// identitySourceFromAPI is the IdentitySource the course activity
// recorder uses (Phase 32.5 pilot task #2). It reads from the same
// context key the api.Identity middleware sets, so user-side
// approvals and agent-side activates produce rows with the right
// actor_type without the handler passing actor info explicitly.
//
// Returning ("", "", false) for missing Identity (test fixtures,
// internal callers) is intentional — the recorder silently skips
// in that case rather than fail the user-visible action.
func identitySourceFromAPI(ctx context.Context) (coursedomain.ActorType, string, bool) {
	id, present := api.IdentityFrom(ctx)
	if !present {
		return "", "", false
	}
	if id.UserID != "" {
		return coursedomain.ActorUser, id.UserID, true
	}
	if id.AgentID != "" {
		return coursedomain.ActorAgent, id.AgentID, true
	}
	return "", "", false
}

// projectIdentitySourceFromAPI is the IdentitySource the project
// activity recorder uses (wiki:agent-project-description). Same
// shape as identitySourceFromAPI but returns the project domain's
// ActorType. v1 only the agent-namespace PATCH writes rows, but the
// recorder resolves the actor generically so a future user-side
// write path produces correct rows without handler changes.
func projectIdentitySourceFromAPI(ctx context.Context) (project.ActorType, string, bool) {
	id, present := api.IdentityFrom(ctx)
	if !present {
		return "", "", false
	}
	if id.UserID != "" {
		return project.ActorUser, id.UserID, true
	}
	if id.AgentID != "" {
		return project.ActorAgent, id.AgentID, true
	}
	return "", "", false
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

// applyStartupBackupSettings merges any persisted operator
// overrides from the backup_settings table into the live
// `*backup.Service`. Phase 32.7 closes the "cold start resets
// schedule to YAML default" surprise that the Phase 28.9 design
// shipped for URL/auth — the cron expression and rotation days
// now survive process restarts. We read the three relevant keys
// directly off the DB and call UpdateConfig with the merged
// result; failures are logged but non-fatal so a corrupt
// settings row can't keep the server from booting.
//
// URL/auth are deliberately not touched here: Phase 28.9 chose
// "YAML wins on cold start" for those two (a deliberate scope
// decision that's out of scope for this task). The merge below
// covers exactly the two knobs Phase 32.7 added to the UI form.
func applyStartupBackupSettings(ctx context.Context, svc *backup.Service, db *sql.DB, logger *zap.Logger) {
	if svc == nil || db == nil {
		return
	}
	repo := sqlite.NewBackupSettingsRepository(db)
	cfg := svc.Config()
	updated := cfg

	if raw, ok, err := repo.GetByKey(ctx, "snapshot_cron"); err == nil && ok {
		s, _ := jsonStringFromRepo(raw)
		if s != "" {
			if _, perr := backup.Parse(s); perr == nil {
				updated.SnapshotCron = s
			} else {
				logger.Warn("ignoring unparseable persisted snapshot_cron; falling back to start-time config",
					zap.String("expr", s), zap.Error(perr))
			}
		}
	} else if err != nil {
		logger.Warn("failed to read persisted snapshot_cron", zap.Error(err))
	}
	if raw, ok, err := repo.GetByKey(ctx, "snapshot_rotation_days"); err == nil && ok {
		var n int
		if uerr := json.Unmarshal(raw, &n); uerr == nil && n >= 0 {
			updated.SnapshotRotationDays = n
		}
	} else if err != nil {
		logger.Warn("failed to read persisted snapshot_rotation_days", zap.Error(err))
	}

	if updated.SnapshotCron != cfg.SnapshotCron || updated.SnapshotRotationDays != cfg.SnapshotRotationDays {
		svc.UpdateConfig(updated)
		logger.Info("applied persisted backup settings",
			zap.String("snapshot_cron", updated.SnapshotCron),
			zap.Int("snapshot_rotation_days", updated.SnapshotRotationDays))
	}
}

// jsonStringFromRepo is the local helper for pulling a JSON
// string back out of the BackupSettingsRepository's blob. The
// repository guarantees the input is valid JSON (the Set path
// validates) so the only failure is "the value isn't a string",
// which we return as "". Mirrors api.jsonString — duplicated
// here so cmd/orenda doesn't reach into internal/api.
func jsonStringFromRepo(raw json.RawMessage) (string, error) {
	var s string
	err := json.Unmarshal(raw, &s)
	return s, err
}

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
		// Phase 32.7: validate the cron expression at startup. A
		// bad expression in config.yaml used to silently fall
		// back to the scheduler's hard-coded default (Phase 7.5
		// never even read the YAML value); now it's wired into
		// the live cfg and the scheduler reads it on every
		// iteration. We refuse to start rather than schedule on
		// a default the operator didn't ask for — the typo is
		// cheaper to fix than the surprise.
		if cfg.Backup.SQLiteSnapshotCron != "" {
			if _, err := backup.Parse(cfg.Backup.SQLiteSnapshotCron); err != nil {
				return fmt.Errorf("backup.sqlite_snapshot_cron: %w", err)
			}
		}
		mirrorSvc = mirror.New(cfg.Backup.MirrorDir)
		backupSvc = backup.New(backup.Config{
			MirrorDir:            cfg.Backup.MirrorDir,
			SnapshotDir:          cfg.Backup.SnapshotDir,
			DBPath:               cfg.ResolveDBPath("."),
			RemoteURL:            cfg.Backup.RemoteURL,
			RemoteAuth:           cfg.Backup.RemoteAuth,
			SnapshotRotationDays: cfg.Backup.SnapshotRotationDays,
			SnapshotCron:         cfg.Backup.SQLiteSnapshotCron,
		}, db)
		// Phase 32.7: merge persisted DB overrides into the
		// live config BEFORE the scheduler goroutine starts so
		// the snapshot loop's first iteration already sees the
		// operator-saved schedule (the previous Phase 28.9
		// design only merged on PUT — restart reset URL/auth to
		// the YAML default, a known surprise that's now closed
		// for the cron/rotation pair). The repo is constructed
		// below alongside the rest of the dependencies, so we
		// can't reuse Dependencies here; instead we read the
		// rows directly off the DB and call UpdateConfig with
		// the merged result. Failures are logged but not fatal
		// — a corrupted settings row shouldn't keep the server
		// from booting.
		applyStartupBackupSettings(cmd.Context(), backupSvc, db, logger)
		scheduler := backup.NewScheduler(backupSvc)
		go scheduler.Run(cmd.Context())
		logger.Info("backup scheduler started",
			zap.String("mirror_dir", cfg.Backup.MirrorDir),
			zap.String("snapshot_dir", cfg.Backup.SnapshotDir),
		)
		// Save the scheduler handle so we can wire the
		// notifier after notifierSvc is constructed below.
		// Phase Wave 4 PR 2: `backup.failed` events fan out
		// from the scheduler's run* helpers.
		pendingNotifier = scheduler
	}

	// Build service layer (Phase 2: task_service.Move; Phase 3.6 adds
	// Claim/Release/Submit/Review — wired with locks repo, Recorder/Comments
	// land in 3.7/3.9).
	hub := ws.NewHub()
	taskLocks := sqlite.NewTaskLockRepository(db)
	commentSvc := commentservice.New(sqlite.NewCommentRepository(db), hub, nil)
	activityRepo := sqlite.NewActivityRepository(db)
	activityRecorder := activityservice.New(activityRepo)
	// Phase 31.4: study service — proposal materialisation (Accept)
	// reads/writes task rows through tasksRepo, so we pass the same
	// instance the task service uses. The live hub + recorder wire
	// the WS fan-out so accept/dismiss events reach the Dashboard
	// tray in real time.
	studySvc := studyservice.New(
		sqlite.NewStudyProposalRepository(db),
		sqlite.NewTaskRepository(db),
		hub,
		nil, // task_service.Recorder would be a circular adapter; accept/dismiss don't audit-row
	)
	taskSvc := taskservice.New(tasksRepo, taskLocks, taskRecorderFor(activityRecorder), commentAdderFor(commentSvc), hub)
	taskSvc.Mirror = mirrorSvc
	taskSvc.Columns = projects // Phase 23.1 + 16.7: WIP lookup + inbox→project filing
	// Phase Wave 4 PR 2: wire CommentLister so the markdown
	// mirror carries the comment thread. The comment service's
	// ListByTarget already exists; we just expose it through the
	// same seam.
	taskSvc.CommentLister = commentSvc

	// wiki:agent-project-description — project activity feed. Wire the
	// recorder so the agent-namespace PATCH /agent/projects/{id} writes
	// project_activity rows (kind=description_changed + before/after
	// diff). IdentitySource reads from api.Identity (the same key the
	// auth middleware uses) so the row carries actor_type=agent.
	projectActivityRepo := sqlite.NewProjectActivityRepository(db)
	projectActivityRecorder := projectservice.NewActivityRecorder(projectActivityRepo)
	projectActivityRecorder.IdentitySource = projectIdentitySourceFromAPI

	// Agent service — Register, Heartbeat, SweepOffline. Exposed
	// through /api/v1/agents (REST) and /api/v1/agent/* (namespace)
	// since Phase 3; the AgentService dep is wired into the router
	// further down.
	agentSvc := agentservice.New(
		sqlite.NewAgentRepository(db),
		users,
		tokenMinterFor(tokens),
		hub,
		nil, // Recorder wired separately when needed (Phase 3.9+)
	)
	_ = agentSvc

	// Event + Time services (Phase 4).
	// Phase 11: events are stored as tasks with start_at/end_at. The
	// eventService facade still exists for API compatibility, but it
	// reads and writes the tasks table now.
	eventSvc := eventservice.New(sqlite.NewTaskRepository(db), hub, nil)
	timeSvc := timeentryservice.New(sqlite.NewTimeEntryRepository(db), hub, nil).
		WithTitles(sqlite.NewTaskRepository(db))

	// Wiki + Search services (Phase 5).
	wikiSvc := wikiservice.New(sqlite.NewWikiRepository(db), hub)
	wikiSvc.Mirror = mirrorSvc
	searchSvc := searchservice.New(sqlite.NewSearchRepository(db), hub)

	// Phase 18: courses (LMS).
	courseRepo := sqlite.NewCourseRepository(db)
	courseActivityRepo := sqlite.NewCourseActivityRepository(db)
	courseSvc := courseservice.New(courseRepo)
	// Phase 27.4: wire the TaskCreator so CreateWithIntent actually
	// spawns a "build the curriculum" task and AnswerQuiz (open)
	// spawns a review task. The adapter keeps the course service
	// from depending on the task service directly — clean seam.
	//
	// Phase 27.9: hub + activityRecorder make spawned tasks visible
	// to live subscribers (review-queue badge, kanban updates).
	courseSvc = courseSvc.WithTaskCreator(courseTaskCreatorAdapter{
		tasksRepo: tasksRepo,
		db:        db,
		hub:       hub,
		recorder:  activityRecorder,
	})
	// Phase 32.5 pilot task #2: course activity feed. Wire the
	// recorder so create / approve / activate / granular CRUD write
	// course_activity rows. IdentitySource reads from api.Identity
	// (the same key the auth middleware uses) so the row carries
	// the right actor_type.
	courseActivityRecorder := courseservice.NewCourseActivityRecorder(courseActivityRepo)
	courseActivityRecorder.IdentitySource = identitySourceFromAPI
	courseSvc = courseSvc.WithActivity(courseActivityRecorder)

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
				return captureToInbox(ctx, db, tasksRepo, "telegram", int64ToString(m.ChatID), m.Text, func(reply string) error {
					return tg.SendReply(ctx, m.ChatID, reply)
				})
			}
		}
	}

	// Phase 30.3: VK Long Poll OnMessage hook. Same inbox-capture flow
	// as Telegram, but routed through vk.Send (peer_id is the same as
	// the message's peer_id). Skipped when no VK bot is registered
	// (e.g., telegram-only install).
	if vk, ok := botRegistry.Get("vk").(*bot.VK); ok && vk != nil {
		vk.OnMessage = func(ctx context.Context, m bot.InboxMessage) error {
			return captureToInbox(ctx, db, tasksRepo, "vk", int64ToString(m.ChatID), m.Text, func(reply string) error {
				return vk.Send(ctx, int64ToString(m.ChatID), bot.Message{Title: reply})
			})
		}
	}

	notifierSvc := notifierservice.New(
		sqlite.NewNotificationRepository(db),
		sqlite.NewBotSubscriptionRepository(db),
		botRegistry,
		hub,
	)

	// Phase Wave 4 PR 2: now that notifierSvc exists, hand it to
	// the backup scheduler. The scheduler is already running; the
	// next tick picks up the notifier. We need a lock or just
	// hope the scheduler hasn't fired yet — for the 5m push
	// interval that's a 5-minute race that's acceptable.
	if pendingNotifier != nil {
		pendingNotifier.WithNotifier(backupFailedAdapter{svc: notifierSvc})
	}

	// Phase Wave 4 PR 2: wire the comment service's notifier
	// seam so `task.commented` events fan out to the task owner.
	// commentSvc was created earlier; we set the deps here.
	commentSvc.Notifier = commentNotifierAdapter{svc: notifierSvc}
	commentSvc.TaskOwnerResolver = taskOwnerResolverAdapter{
		tasks:    tasksRepo,
		projects: projects,
		users:    usersRaw,
	}

	// Phase 22.3 follow-up: hand the API layer a BindCodesSource so
	// the /bots/telegram/bind endpoint can resolve one-shot codes
	// minted by the bot on /start. The bot may not be running
	// (token unset), in which case we pass nil — the API returns
	// 503 with a hint and the UI shows the offline state.
	var bindCodes api.BindCodesSource
	if tg, ok := botRegistry.Get("telegram").(*bot.Telegram); ok && tg != nil {
		bindCodes = tg.BindCodes
	}

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
	router := api.NewRouter(&api.Dependencies{
		Logger:      logger,
		Signer:      signer,
		Users:       users,
		Projects:    projects,
		Tasks:       tasksRepo,
		Tokens:      tokens,
		TaskService: taskSvc,
		// Phase 15: wire task_locks Holder() so 409 lock_taken
		// and /tasks/:id/context can surface the current holder
		// instead of a bare "lock_taken".
		TaskLockHolder: taskLocks,
		AgentService:   agentSvc,
		Agents:         sqlite.NewAgentRepository(db),
		Comments:       commentSvc,
		Attachments: attachmentServiceFor(attachmentsvc.New(sqlite.NewAttachmentRepository(db), attachmentsvc.Config{
			UploadDir:    cfg.ResolveUploadsDir(cwdOr(absCfg, ".")),
			MaxSizeBytes: int64(cfg.Uploads.MaxSizeMB) * 1024 * 1024,
			AllowedMimes: cfg.Uploads.AllowedMimes,
		}, hub)),
		Activities: activityRepo,
		// Phase 28.5: comment + attachment handlers write task_activity
		// rows through this recorder. *activityservice.Recorder
		// satisfies api.ActivityRecorder structurally (same method
		// signature) — no explicit adapter needed.
		ActivityRecorder:   activityRecorder,
		EventService:       eventSvc,
		TimeService:        timeSvc,
		WikiService:        wikiSvc,
		SearchService:      searchSvc,
		Courses:            courseRepo,
		CourseService:      courseSvc,
		CourseActivityRepo: courseActivityRepo,
		// wiki:agent-project-description — write side for project_activity
		// rows. The agent-namespace PATCH /agent/projects/{id} emits a
		// description_changed row through this seam.
		ProjectActivityRecorder: projectActivityRecorder,
		// Phase 31: study service wires the user-side accept/dismiss
		// + the agent-side propose. nil-safe in handlers (they check
		// before calling) but the production binary must wire it
		// here or every study endpoint returns 503.
		StudyService: studySvc,
		// Phase 32.11: dashboard chat thread persistence.
		ChatMessages: sqlite.NewChatMessageRepository(db),
		Notifier:     notifierSvc,
		Backup:       backupSvc,
		// Phase 28.1 polish.1: UI-editable override repo. PUT
		// /api/v1/backups/settings writes here; GET merges it over
		// the in-memory cfg (see handlers_backup.go). Settings take
		// effect on the next process restart — `*backup.Service`
		// is wired from cfg above and stays immutable.
		BackupSettings: sqlite.NewBackupSettingsRepository(db),
		SyncOps:        sqlite.NewSyncOpsRepository(db),
		BotCallback:    botCallback,
		BotBindCodes:   bindCodes,
		// Phase 10 Test send UI: the live bot registry is what the
		// /api/v1/bots/test handler dispatches through. nil is fine
		// for partial-router test fixtures that don't construct
		// bots — the handler returns 503 in that case.
		BotRegistry:  botRegistry,
		WSHub:        hub,
		CookieName:   cfg.Auth.CookieName,
		CookieSecure: cfg.Auth.CookieSecure,
		JWTTTL:       cfg.Auth.JWTTTL,
		DBPath:       cfg.ResolveDBPath("."),
		// Phase 28.8: rate-limit knobs from config (already merged
		// with env by the time Load returns).
		RateLimitAnonBurst:  cfg.RateLimit.AnonBurst,
		RateLimitAnonPerSec: cfg.RateLimit.AnonPerSec,
		RateLimitAuthBurst:  cfg.RateLimit.AuthBurst,
		RateLimitAuthPerSec: cfg.RateLimit.AuthPerSec,
		// Phase 32.2: advertise which optional features this binary
		// actually wired. Pre-fix, Capabilities{} zero-valued (every
		// flag false) regardless of what's wired below — clients
		// couldn't tell if FTS / WebSocket / Backup were available.
		Capabilities: api.Capabilities{
			Auth:      true, // cookie + bearer wired above
			RESTTasks: true, // tasksRepo wired above
			WebSocket: hub != nil,
			Backup:    backupSvc != nil,
			Bots:      botRegistry != nil,
			FTS:       searchSvc != nil,
			PWA:       true, // web/dist embedded at build time (27.1)
		},
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

	// Phase 30.5: weekly digest scheduler. Ticks every 7 days
	// (configurable via cfg.Notifier.DigestInterval — default 168h).
	// The scheduler queries the storage layer per-owner for the
	// period stats, renders via notifier.RenderWeeklyDigest, and
	// pushes a "digest.weekly" event through the same notifier
	// pipeline as any other notification — so every bot the
	// operator has subscribed to (Telegram, VK, Email, Webhook,
	// Console) gets the digest. Disabled when DigestInterval is
	// zero or negative (operator opt-out).
	if cfg.Notifier.DigestInterval > 0 {
		digest := &digestScheduler{
			interval: cfg.Notifier.DigestInterval,
			logger:   logger,
			db:       db,
			users:    userListerAdapter{repo: sqlite.NewUserRepository(db)},
			notifier: notifierDigestAdapter{svc: notifierSvc},
		}
		go digest.Run(ctx)
		logger.Info("weekly digest scheduler started",
			zap.Duration("interval", cfg.Notifier.DigestInterval))
	}

	// Phase 28.6: opt-in pprof listener for live debugging. Off by
	// default — pprof endpoints expose heap, goroutine, and CPU
	// state that are an information leak on any reachable port.
	// When enabled, a SECOND listener runs on cfg.Server.PProfAddr
	// bound to http.DefaultServeMux (net/http/pprof registers itself
	// there on import). Loopback-only by design — operators who
	// want remote profiling should set up an ssh tunnel rather
	// than bind 0.0.0.0.
	var pprofSrv *http.Server
	if cfg.Server.DebugPProf {
		pprofSrv = &http.Server{
			Addr:              cfg.Server.PProfAddr,
			Handler:           http.DefaultServeMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			logger.Warn("pprof listening (debug only)",
				zap.String("addr", cfg.Server.PProfAddr),
				zap.String("hint", "stop with cfg.server.debug_pprof=false"),
			)
			if err := pprofSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Warn("pprof listener stopped", zap.Error(err))
			}
		}()
	}

	serverErr := make(chan error, 1)
	go func() {
		// Phase 28.20: db_path in the startup log makes "which instance is
		// this?" answerable from `journalctl --user -u orenda` alone —
		// important when dev (./data/) and usage (~/.local/share/orenda/)
		// both run on the same box under the same operator.
		logger.Info("http listening",
			zap.String("addr", srv.Addr),
			zap.String("db_path", cfg.ResolveDBPath(".")),
		)
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
	// Phase 28.6: shut the pprof listener down too. We bound the
	// shutdown to the same timeout — the pprof server is debug-only
	// and a few in-flight profile requests can wait for the
	// regular deadline.
	if pprofSrv != nil {
		if err := pprofSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("pprof shutdown", zap.Error(err))
		}
	}
	// Phase 28.5: tell the bots to stop too. Without this, long-poll
	// transports (Telegram) keep their goroutines alive past
	// http.Server.Shutdown — the process gets SIGKILL'd at the
	// end of the shutdown timeout while updates are mid-flight,
	// which surfaces as "context canceled" errors on the upstream
	// bot API. Best-effort: a bot that fails to Stop doesn't fail
	// the whole shutdown — log and continue.
	for _, b := range botRegistry.List() {
		name := b.Name()
		if err := b.Stop(shutdownCtx); err != nil {
			logger.Warn("bot stop failed", zap.String("bot", name), zap.Error(err))
		}
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
		// Phase Wave 4 (down-migrations PR): actually roll back
		// the most recent migration via its .down.sql companion.
		// The runner handles the irreversible marker — those
		// migrations surface ErrMigrationIrreversible instead.
		if err := sqlite.MigrateDown(cmd.Context(), db, sqlite.MigrationsFS, "migrations"); err != nil {
			if errors.Is(err, sqlite.ErrMigrationIrreversible) {
				logger.Warn("migrate down refused", zap.String("reason", err.Error()))
			} else if errors.Is(err, sqlite.ErrNoDownFile) {
				logger.Warn("migrate down: no .down.sql written yet", zap.String("hint", err.Error()))
			}
			return fmt.Errorf("migrate down: %w", err)
		}
		logger.Info("migrate down complete", zap.String("rolled_back", "last migration"))
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
// ----------------------------------------------------------------------------
// Phase 30.5: weekly digest scheduler adapters.
// ----------------------------------------------------------------------------

// userListerAdapter wraps the SQLite UserRepository for the digest
// scheduler's narrow ownerLister interface. The user table doesn't
// store an Active column — every row is by definition active
// (the only "deactivation" path is Delete, which removes the row
// entirely), so we treat every row as Active=true.
type userListerAdapter struct {
	repo *sqlite.UserRepo
}

func (a userListerAdapter) ListAll(ctx context.Context) ([]ownerRecord, error) {
	users, err := a.repo.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ownerRecord, 0, len(users))
	for _, u := range users {
		out = append(out, ownerRecord{ID: u.ID, Active: true})
	}
	return out, nil
}

// notifierDigestAdapter wraps the notifier service so the digest
// scheduler's narrow digestNotifier interface can be satisfied
// without pulling the entire notifier package surface.
type notifierDigestAdapter struct {
	svc *notifierservice.Service
}

func (a notifierDigestAdapter) Notify(ctx context.Context, e notifyEvent) error {
	return a.svc.Notify(ctx, notifierservice.Event{
		Type:   e.Type,
		UserID: e.UserID,
		Title:  e.Title,
		Body:   e.Body,
		Link:   e.Link,
		Meta:   e.Meta,
	})
}

var _ = time.Second

// captureToInbox is the shared Phase 21/30.3 inbox-capture flow,
// reused by the Telegram and VK OnMessage hooks. It looks up the user
// subscribed to (botType, targetAddress), creates an inbox task with
// the message text as the title (truncated to 200 chars), and invokes
// `reply` with the friendly "✅ Captured to Inbox" string.
//
// No subscription → silently drop (the bot receives messages from
// strangers too; we don't reply, but we don't surface the spam
// either). Reply failures are swallowed — there's nothing the user
// can do from inside Telegram/VK about a transient transport error,
// and the task is already on disk.
//
// Lives outside runServe as a free function so both bot hooks share
// the same code path. Dependencies are passed explicitly rather than
// captured as closures (Go closures over *sql.DB / *sqlite.TaskRepo
// work but are harder to test in isolation).
func captureToInbox(
	ctx context.Context,
	db *sql.DB,
	tasks task.Repository,
	botType, targetAddress, text string,
	reply func(string) error,
) error {
	subRepo := sqlite.NewBotSubscriptionRepository(db)
	subs, err := subRepo.ListByBotType(ctx, botType)
	var owner string
	if err == nil {
		for _, s := range subs {
			if s.Enabled && s.TargetAddress == targetAddress {
				owner = s.UserID
				break
			}
		}
	}
	if owner == "" {
		return nil
	}
	title := strings.TrimSpace(text)
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
	if err := tasks.Create(ctx, tr); err != nil {
		return err
	}
	_ = reply("✅ Captured to Inbox")
	return nil
}
