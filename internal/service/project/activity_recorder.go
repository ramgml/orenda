// Package project provides the ActivityRecorder implementation
// that the agent-projects handler uses to write audit rows.
//
// Wiki: agent-project-description. Mirrors service/course/course_activity_recorder.go
// (Phase 32.5 pilot task #2) but scoped to project mutations.
// v1 ships with one recorder kind: description_changed when an agent
// PATCHes a project description. Future kinds land as new methods
// (or as an explicit list parameter) — see project.ActivityKind for
// the closed set.
package project

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/project"
)

// IdentitySource is the minimal slice of api.IdentityFrom that
// ActivityRecorder needs. Keeping the dependency one-method
// makes test stubs trivial.
//
// Returning ("", "", false) for missing Identity (test fixtures,
// internal callers without an identity) is intentional — the
// recorder silently skips in that case rather than fail the
// user-visible action.
type IdentitySource func(ctx context.Context) (actorType project.ActorType, actorID string, ok bool)

// defaultIdentitySource reads from the api package's ctx key. Set
// via WithIdentitySource; defaults to a no-op that returns ok=false
// so the recorder silently skips when no identity is present.
var defaultIdentitySource IdentitySource = func(ctx context.Context) (project.ActorType, string, bool) {
	return "", "", false
}

// ActivityRepo is the persistence surface.
// *sqlite.ProjectActivityRepository satisfies this; declared here
// to keep the recorder test-friendly.
type ActivityRepo interface {
	Create(ctx context.Context, a *project.Activity) error
}

// ActivityRecorder writes project_activity rows. The api layer holds
// it behind the api.ProjectActivityRecorder interface (the concrete
// type satisfies it structurally via RecordProjectAuto).
type ActivityRecorder struct {
	Repo           ActivityRepo
	IdentitySource IdentitySource
}

// NewActivityRecorder returns a wired recorder. IdentitySource
// defaults to defaultIdentitySource (no-op) — callers (main.go)
// set it to api.IdentityFrom so production requests produce real
// rows.
func NewActivityRecorder(repo ActivityRepo) *ActivityRecorder {
	return &ActivityRecorder{Repo: repo, IdentitySource: defaultIdentitySource}
}

// RecordProjectAuto writes one row, resolving actor (user vs agent)
// from context via IdentitySource. Skips silently when no Identity
// is in context (test fixture, internal call). Returns an error
// only when the row was attempted but persistence failed — the
// handler decides whether to fail the request or log-and-continue
// (we log-and-continue so audit gaps don't fail user actions).
func (r *ActivityRecorder) RecordProjectAuto(ctx context.Context, projectID string, kind project.ActivityKind, payload string) error {
	if r == nil || r.Repo == nil {
		return nil
	}
	var actorType project.ActorType
	var actorID string
	if r.IdentitySource != nil {
		actorType, actorID, _ = r.IdentitySource(ctx)
	}
	// Silently skip when no identity — internal callers, tests
	// without an Identity stub.
	if actorID == "" {
		return nil
	}
	a := &project.Activity{
		ID:        newProjectActivityID(),
		ProjectID: projectID,
		ActorType: actorType,
		ActorID:   actorID,
		Kind:      kind,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	}
	if err := a.Validate(); err != nil {
		return fmt.Errorf("project activity: record: %w", err)
	}
	return r.Repo.Create(ctx, a)
}

// newProjectActivityID returns a UUIDv7-shaped identifier without
// pulling in the google/uuid package. Phase 1 already uses google/uuid
// elsewhere; this local helper mirrors course_activity_recorder.go
// and keeps the recorder test-friendly (no extra import to satisfy).
func newProjectActivityID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is effectively impossible on Linux;
		// fall back to time-based prefix so we still get a unique
		// ID instead of crashing the request.
		ts := time.Now().UnixNano()
		binary.BigEndian.PutUint64(b[:8], uint64(ts))
	}
	// UUIDv7 layout: high 48 bits = unix ms; version + variant nibbles.
	ms := uint64(time.Now().UnixMilli())
	binary.BigEndian.PutUint64(b[:8], ms)
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(b[0:4]),
		binary.BigEndian.Uint16(b[4:6]),
		binary.BigEndian.Uint16(b[6:8]),
		binary.BigEndian.Uint16(b[8:10]),
		b[10:16],
	)
}
