// Repository constructors re-exported from the sqlite driver so that
// production call sites never import internal/storage/sqlite directly.
//
// These are plain aliases: same signatures, same concrete types, zero
// behaviour change. When a second driver lands, the aliases become the
// natural place to dispatch on the configured dialect.
package storage

import (
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
	NewSearchRepository          = sqlite.NewSearchRepository
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

// Migration runner sentinels, re-exported for the CLI's
// errors.Is branches.
var (
	// ErrMigrationIrreversible is returned when a down-migration is
	// marked irreversible.
	ErrMigrationIrreversible = sqlite.ErrMigrationIrreversible
	// ErrNoDownFile is returned when a migration has no .down.sql yet.
	ErrNoDownFile = sqlite.ErrNoDownFile
)
