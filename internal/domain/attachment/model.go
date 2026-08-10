// Package attachment holds the Attachment domain entity.
//
// Attachments are files attached to a target (typically a task). The
// filesystem path is recorded as data/uploads/YYYY/MM/{uuid}-{sanitized}
// per PLAN#3.8.
package attachment

import (
	"errors"
	"time"
)

// TargetType mirrors comment.TargetType for consistency.
type TargetType string

const (
	TargetTask    TargetType = "task"
	TargetPage    TargetType = "page"
	TargetEvent   TargetType = "event"
	TargetProject TargetType = "project"
)

// UploaderType identifies who uploaded the attachment.
type UploaderType string

const (
	UploaderUser  UploaderType = "user"
	UploaderAgent UploaderType = "agent"
)

// Sentinel errors.
var (
	ErrNotFound     = errors.New("attachment: not found")
	ErrInvalidInput = errors.New("attachment: invalid input")
)

// Attachment is the canonical entity.
type Attachment struct {
	ID             string       `json:"id"`
	TargetType     TargetType   `json:"target_type"`
	TargetID       string       `json:"target_id"`
	Filename       string       `json:"filename"`
	Mime           string       `json:"mime"`
	Size           int64        `json:"size"`
	Path           string       `json:"path"`
	SHA256         string       `json:"sha256"`
	UploadedByType UploaderType `json:"uploaded_by_type"`
	UploadedByID   string       `json:"uploaded_by_id"`
	CreatedAt      time.Time    `json:"created_at"`
}

// Validate returns an error if the Attachment fields are inconsistent.
func (a *Attachment) Validate() error {
	if a.TargetID == "" || a.Filename == "" || a.Path == "" {
		return ErrInvalidInput
	}
	if a.Size <= 0 {
		return ErrInvalidInput
	}
	if len(a.SHA256) != 64 {
		return ErrInvalidInput
	}
	if a.UploadedByID == "" {
		return ErrInvalidInput
	}
	if a.TargetType == "" {
		a.TargetType = TargetTask
	}
	if a.UploadedByType == "" {
		a.UploadedByType = UploaderUser
	}
	return nil
}
