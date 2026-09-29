package storage

// Repository constructors re-exported from the sqlite driver so that
// production call sites never import internal/storage/sqlite directly.
//
// These are plain aliases: same signatures, same concrete types, zero
// behaviour change. When a second driver lands, the aliases become the
// natural place to dispatch on the configured dialect.

import (
	"database/sql"

	"github.com/ramgml/orenda/internal/service/search"
	"github.com/ramgml/orenda/internal/storage/postgres"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Constructor aliases over the sqlite driver.
var (
	NewActivityRepository        = sqlite.NewActivityRepository
	NewAgentRepository           = sqlite.NewAgentRepository
	NewAPITokenRepository        = sqlite.NewAPITokenRepository
	NewAttachmentRepository      = sqlite.NewAttachmentRepository
	NewBackupSettingsRepository  = sqlite.NewBackupSettingsRepository
	NewBotSubscriptionRepository = sqlite.NewBotSubscriptionRepository
	NewChatMessageRepository     = sqlite.NewChatMessageRepository
	NewChatThreadRepository      = sqlite.NewChatThreadRepository
	NewCommentRepository         = sqlite.NewCommentRepository
	NewCourseActivityRepository  = sqlite.NewCourseActivityRepository
	NewCourseRepository          = sqlite.NewCourseRepository
	NewLessonReviewRepository    = sqlite.NewLessonReviewRepository
	NewNotificationRepository    = sqlite.NewNotificationRepository
	NewProjectActivityRepository = sqlite.NewProjectActivityRepository
	NewProjectRepository         = sqlite.NewProjectRepository
	NewStudyProposalRepository   = sqlite.NewStudyProposalRepository
	NewSyncOpsRepository         = sqlite.NewSyncOpsRepository
	NewTaskLockRepository        = sqlite.NewTaskLockRepository
	NewTaskRepository            = sqlite.NewTaskRepository
	NewTimeEntryRepository       = sqlite.NewTimeEntryRepository
	NewTutorMessageRepository    = sqlite.NewTutorMessageRepository
	NewUserRepository            = sqlite.NewUserRepository
	NewWikiRepository            = sqlite.NewWikiRepository

	// EnsureChatActor is the T9 runtime bootstrap for the synthetic
	// "chat" actor (migrations must not create users).
	EnsureChatActor = sqlite.EnsureChatActor
)

// Type aliases for concrete driver types that leak into wiring
// signatures; consumers reference the neutral names.
type (
	// APITokenRepo is the concrete api_tokens repository.
	APITokenRepo = sqlite.APITokenRepo
	// UserRepo is the concrete users repository.
	UserRepo = sqlite.UserRepo
	// BackupSetting is one backup_settings row.
	BackupSetting = sqlite.BackupSetting
	// BackupSettingsRepository is the backup_settings persistence
	// surface the API depends on (kept interface-shaped here so the
	// router never names the driver package).
	BackupSettingsRepository = sqlite.BackupSettingsRepository
)

// NewSearchRepository returns the full-text search repository for the
// dialect the handle speaks (T365). This is the one constructor that
// grew from a plain alias into a real dispatch — exactly as the package
// comment predicted — because full-text search is the first subsystem
// with per-dialect implementations: FTS5 (MATCH/bm25/snippet) on sqlite,
// generated tsvector columns + phraseto_tsquery/ts_headline/ts_rank on
// postgres (migration 002_search). Both satisfy service/search.Repository;
// the domain interface and the sqlite implementation are unchanged.
func NewSearchRepository(dialect Dialect, db *sql.DB) search.Repository {
	if dialect == DialectPostgres {
		return postgres.NewSearchRepository(db)
	}
	return sqlite.NewSearchRepository(db)
}

// Migration runner sentinels, re-exported for the CLI's
// errors.Is branches.
var (
	// ErrMigrationIrreversible is returned when a down-migration is
	// marked irreversible.
	ErrMigrationIrreversible = sqlite.ErrMigrationIrreversible
	// ErrNoDownFile is returned when a migration has no .down.sql yet.
	ErrNoDownFile = sqlite.ErrNoDownFile
)
