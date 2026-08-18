// Package service/course — CourseActivityRecorder implementation.
// Phase 32.5 pilot task #2.
//
// Implements the ActivityRecorder seam declared in course.go.
// Reads actor identity (user vs agent) from the request context
// (same key the api.Identity middleware uses), generates a row,
// persists via the sqlite-backed repo.
//
// IdentityFrom returning nothing (no actor in ctx) is silently
// treated as "skip" — internal callers (tests, future CLI) don't
// have to fake an Identity just to write a row. User-visible
// actions always go through middleware, so Identity is always set
// in production paths.

package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/course"
)

// IdentitySource is the minimal slice of api.IdentityFrom that
// CourseActivityRecorder needs. Keeping the dependency one-method
// makes test stubs trivial.
type IdentitySource func(ctx context.Context) (actorType course.ActorType, actorID string, ok bool)

// defaultIdentitySource reads from the api package's ctx key. Set
// via WithIdentitySource; defaults to a no-op that returns ok=false
// so the recorder silently skips when no identity is present.
var defaultIdentitySource IdentitySource = func(ctx context.Context) (course.ActorType, string, bool) {
	return "", "", false
}

// CourseActivityRepo is the persistence surface. *sqlite.CourseActivityRepository
// satisfies this; declared here to keep the recorder test-friendly.
type CourseActivityRepo interface {
	Create(ctx context.Context, a *course.Activity) error
}

// CourseActivityRecorder writes course_activity rows for course
// mutations. Threaded through courseSvc.WithActivity.
type CourseActivityRecorder struct {
	Repo           CourseActivityRepo
	IdentitySource IdentitySource
}

// NewCourseActivityRecorder returns a wired recorder. IdentitySource
// defaults to defaultIdentitySource (no-op) — callers (main.go)
// set it to api.IdentityFrom so production requests produce real
// rows.
func NewCourseActivityRecorder(repo CourseActivityRepo) *CourseActivityRecorder {
	return &CourseActivityRecorder{Repo: repo, IdentitySource: defaultIdentitySource}
}

// RecordCourseAuto writes one row, resolving actor (user vs agent)
// from context via IdentitySource. Skips silently when no Identity
// is in context (test fixture, internal call). Returns an error
// only when the row was attempted but persistence failed — caller
// decides whether to fail the user-visible action or swallow
// (we swallow at the call site; audit gaps shouldn't fail user
// actions).
func (r *CourseActivityRecorder) RecordCourseAuto(ctx context.Context, courseID string, kind course.ActivityKind, payload string) error {
	if r == nil || r.Repo == nil {
		return nil
	}
	var actorType course.ActorType
	var actorID string
	if r.IdentitySource != nil {
		actorType, actorID, _ = r.IdentitySource(ctx)
	}
	// Silently skip when no identity — internal callers, tests
	// without an Identity stub.
	if actorID == "" {
		return nil
	}
	a := &course.Activity{
		ID:        newActivityID(),
		CourseID:  courseID,
		ActorType: actorType,
		ActorID:   actorID,
		Kind:      kind,
		Payload:   payload,
		CreatedAt: time.Now().UTC(),
	}
	if err := a.Validate(); err != nil {
		return fmt.Errorf("course activity: record: %w", err)
	}
	return r.Repo.Create(ctx, a)
}

// newActivityID returns a UUIDv7-shaped identifier without pulling
// in the google/uuid package (kept light to avoid an extra dep in
// test fixtures that import this file). Phase 1 already uses
// google/uuid elsewhere — this is a local helper for the recorder
// specifically because the service package shouldn't take a hard
// dep on uuid for tests.
func newActivityID() string {
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
