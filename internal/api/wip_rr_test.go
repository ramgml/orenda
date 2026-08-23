package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
	eventservice "github.com/ramgml/orenda/internal/service/event"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Phase 23.1 + 16.4: WIP-limit actually blocks a move when the
// target column is full. The fixture wires `taskSvc.Columns = projects`
// (the seam added by Wave 0) so the new lookupWIPLimit reads the
// column's wip_limit.
func TestIntegration_MoveRejectsWhenColumnWIPFull(t *testing.T) {
	t.Parallel()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email: "wip@x.com", PasswordHash: mustHashFast(t), DisplayName: "W",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	repo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	taskSvc := taskservice.New(repo, sqlite.NewTaskLockRepository(db), nil, nil, hub)
	// Phase 23.1: real WIP lookup needs the project repository.
	taskSvc.Columns = projRepo

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	router := api.NewRouter(&api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    projRepo,
		Tasks:       repo,
		Tokens:      sqlite.NewAPITokenRepository(db),
		TaskService: taskSvc,
		WSHub:       hub,
		CookieName:  "orenda_session",
	})

	// Login.
	body, _ := json.Marshal(map[string]string{"email": "wip@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	authed := func(method, path string, body any) *httptest.ResponseRecorder {
		buf, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	// Create project + read board.
	rr = authed(http.MethodPost, "/api/v1/projects", map[string]any{"name": "WIP"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var p struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))

	rr = authed(http.MethodGet, "/api/v1/projects/"+p.ID+"/board", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var b struct {
		Columns []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"columns"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &b))
	require.GreaterOrEqual(t, len(b.Columns), 2)
	backlog := b.Columns[0]
	todo := b.Columns[1]

	// Set wip_limit = 1 on the TODO column.
	rr = authed(http.MethodPatch, "/api/v1/columns/"+todo.ID, map[string]any{
		"wip_limit": 1,
	})
	require.Equal(t, http.StatusOK, rr.Code)

	// Create a task IN TODO first (uses the slot).
	rr = authed(http.MethodPost, "/api/v1/projects/"+p.ID+"/tasks", map[string]any{
		"title": "occupant", "column_id": todo.ID,
	})
	require.Equal(t, http.StatusCreated, rr.Code)

	// Create a second task in BACKLOG; moving it to TODO should fail (422).
	rr = authed(http.MethodPost, "/api/v1/projects/"+p.ID+"/tasks", map[string]any{
		"title": "intruder", "column_id": backlog.ID,
	})
	require.Equal(t, http.StatusCreated, rr.Code)
	var tr struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &tr))

	rr = authed(http.MethodPost, "/api/v1/tasks/"+tr.ID+"/move", map[string]any{
		"column_id": todo.ID,
	})
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code, "move into full column must 422")
	assert.Contains(t, rr.Body.String(), "wip_limit")
}

// Phase 23.3: listEventsHandler expands recurring events via
// Service.ExpandRecurrence so a DAILY master emits N occurrences
// inside [from, to).
// Task 38: POST /api/v1/events must return 422 (not panic) when
// start_at or end_at is missing from the request body. The old code
// dereferenced parseOptionalTime() without a nil check, causing a
// nil pointer dereference on any body that omitted those fields.
func TestCreateEvent_MissingTimesReturns422(t *testing.T) {
	t.Parallel()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email: "task38@x.com", PasswordHash: mustHashFast(t), DisplayName: "T38",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	repo := sqlite.NewTaskRepository(db)
	taskSvc := taskservice.New(repo, sqlite.NewTaskLockRepository(db), nil, nil, hub)
	eventSvc := eventservice.New(repo, hub, nil)
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	router := api.NewRouter(&api.Dependencies{
		Logger:       zap.NewNop(),
		Signer:       signer,
		Users:        users,
		Projects:     sqlite.NewProjectRepository(db),
		Tasks:        repo,
		Tokens:       sqlite.NewAPITokenRepository(db),
		TaskService:  taskSvc,
		EventService: eventSvc,
		WSHub:        hub,
		CookieName:   "orenda_session",
	})

	body, _ := json.Marshal(map[string]string{"email": "task38@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing both", map[string]any{"title": "Test"}},
		{"missing start_at only", map[string]any{"title": "Test", "end_at": "2026-08-21T15:00:00Z"}},
		{"missing end_at only", map[string]any{"title": "Test", "start_at": "2026-08-21T14:00:00Z"}},
		{"wrong field names", map[string]any{"title": "Test", "date": "2026-08-21", "start_time": "14:00", "end_time": "15:00"}},
		{"empty strings", map[string]any{"title": "Test", "start_at": "", "end_at": ""}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, _ := json.Marshal(tc.body)
			r := httptest.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewReader(buf))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, http.StatusUnprocessableEntity, w.Code,
				"expected 422, got %d: %s", w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "start_at")
		})
	}
}

// Task 38: PATCH /api/v1/events/{id} with an unparseable (but non-empty)
// start_at must return 422, not panic. The old code dereferenced
// parseOptionalTime() inside the "if in.StartAt != "" guard, which
// still panics when parseOptionalTime returns nil for garbage input.
func TestUpdateEvent_InvalidTimestampReturns422(t *testing.T) {
	t.Parallel()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email: "task38u@x.com", PasswordHash: mustHashFast(t), DisplayName: "T38U",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	repo := sqlite.NewTaskRepository(db)
	taskSvc := taskservice.New(repo, sqlite.NewTaskLockRepository(db), nil, nil, hub)
	eventSvc := eventservice.New(repo, hub, nil)
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	router := api.NewRouter(&api.Dependencies{
		Logger:       zap.NewNop(),
		Signer:       signer,
		Users:        users,
		Projects:     sqlite.NewProjectRepository(db),
		Tasks:        repo,
		Tokens:       sqlite.NewAPITokenRepository(db),
		TaskService:  taskSvc,
		EventService: eventSvc,
		WSHub:        hub,
		CookieName:   "orenda_session",
	})

	body, _ := json.Marshal(map[string]string{"email": "task38u@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	authed := func(method, path string, body any) *httptest.ResponseRecorder {
		buf, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	// Create a valid event first.
	start := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	end := start.Add(30 * time.Minute)
	rr = authed(http.MethodPost, "/api/v1/events", map[string]any{
		"title":    "Valid",
		"start_at": start.Format(time.RFC3339),
		"end_at":   end.Format(time.RFC3339),
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))
	require.NotEmpty(t, created.ID)

	// PATCH with garbage start_at → must 422, not panic.
	rr = authed(http.MethodPatch, "/api/v1/events/"+created.ID, map[string]any{
		"start_at": "not-a-timestamp",
	})
	require.Equal(t, http.StatusUnprocessableEntity, rr.Code,
		"expected 422, got %d: %s", rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "start_at")

	// PATCH with garbage end_at → same.
	rr = authed(http.MethodPatch, "/api/v1/events/"+created.ID, map[string]any{
		"end_at": "definitely-not-valid",
	})
	require.Equal(t, http.StatusUnprocessableEntity, rr.Code,
		"expected 422, got %d: %s", rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "end_at")
}

func TestIntegration_ListEvents_ExpandsRecurrence(t *testing.T) {
	t.Parallel()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email: "rr@x.com", PasswordHash: mustHashFast(t), DisplayName: "R",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	repo := sqlite.NewTaskRepository(db)
	taskSvc := taskservice.New(repo, sqlite.NewTaskLockRepository(db), nil, nil, hub)
	eventSvc := eventservice.New(repo, hub, nil)
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	router := api.NewRouter(&api.Dependencies{
		Logger:       zap.NewNop(),
		Signer:       signer,
		Users:        users,
		Projects:     sqlite.NewProjectRepository(db),
		Tasks:        repo,
		Tokens:       sqlite.NewAPITokenRepository(db),
		TaskService:  taskSvc,
		EventService: eventSvc,
		WSHub:        hub,
		CookieName:   "orenda_session",
	})

	body, _ := json.Marshal(map[string]string{"email": "rr@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	authed := func(method, path string, body any) *httptest.ResponseRecorder {
		buf, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	// Create a project + column (the event needs a project_id FK).
	rr = authed(http.MethodPost, "/api/v1/projects", map[string]any{"name": "RR"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var p struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))

	// Create a DAILY recurring event starting today at 09:00 UTC.
	start := time.Now().UTC().Truncate(time.Hour)
	end := start.Add(30 * time.Minute)
	rr = authed(http.MethodPost, "/api/v1/events", map[string]any{
		"title":      "Stand-up",
		"start_at":   start.Format(time.RFC3339),
		"end_at":     end.Format(time.RFC3339),
		"project_id": p.ID,
		"recurrence": "FREQ=DAILY;INTERVAL=1",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	// List events over a 7-day window — expect ~7 occurrences.
	from := start.Add(-time.Hour)
	to := start.Add(7 * 24 * time.Hour)
	q := "?from=" + from.Format(time.RFC3339) + "&to=" + to.Format(time.RFC3339)
	rr = authed(http.MethodGet, "/api/v1/events"+q, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var list struct {
		Events []struct {
			ID         string `json:"id"`
			StartAt    string `json:"start_at"`
			Recurrence string `json:"recurrence,omitempty"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))

	// Synthetic ids are "masterId::N". The master lives at index 0
	// (with recurrence intact) and indices 1..6 carry no recurrence.
	assert.GreaterOrEqual(t, len(list.Events), 6, "expected at least 6 daily occurrences, got %d", len(list.Events))
	for _, e := range list.Events {
		assert.Contains(t, e.ID, "::", "occurrence id should be synthetic")
	}
	// First occurrence carries the rule (so the UI's "edit series" works).
	assert.NotEmpty(t, list.Events[0].Recurrence, "first occurrence must carry the RRULE")
	// Subsequent occurrences are pure displays.
	for _, e := range list.Events[1:] {
		assert.Empty(t, e.Recurrence, "non-first occurrence must not carry the RRULE")
	}
}

// Task 39: synthetic occurrence ids (::N) must round-trip to the master.
// PATCH /api/v1/events/{masterID::1} must resolve to the master and
// succeed; GET must return the master; PATCH unknown id → 404 (not 500).
func TestIntegration_SyntheticOccurrenceID_RoundTripsToMaster(t *testing.T) {
	t.Parallel()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email: "t39@x.com", PasswordHash: mustHashFast(t), DisplayName: "T39",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	repo := sqlite.NewTaskRepository(db)
	taskSvc := taskservice.New(repo, sqlite.NewTaskLockRepository(db), nil, nil, hub)
	eventSvc := eventservice.New(repo, hub, nil)
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	router := api.NewRouter(&api.Dependencies{
		Logger:       zap.NewNop(),
		Signer:       signer,
		Users:        users,
		Projects:     sqlite.NewProjectRepository(db),
		Tasks:        repo,
		Tokens:       sqlite.NewAPITokenRepository(db),
		TaskService:  taskSvc,
		EventService: eventSvc,
		WSHub:        hub,
		CookieName:   "orenda_session",
	})

	body, _ := json.Marshal(map[string]string{"email": "t39@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	authed := func(method, path string, body any) *httptest.ResponseRecorder {
		buf, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	// Create a project (event needs a project_id FK).
	rr = authed(http.MethodPost, "/api/v1/projects", map[string]any{"name": "T39"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var p struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))

	// Create a DAILY recurring event.
	start := time.Now().UTC().Truncate(time.Hour)
	end := start.Add(30 * time.Minute)
	rr = authed(http.MethodPost, "/api/v1/events", map[string]any{
		"title":      "Daily",
		"start_at":   start.Format(time.RFC3339),
		"end_at":     end.Format(time.RFC3339),
		"project_id": p.ID,
		"recurrence": "FREQ=DAILY;INTERVAL=1",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	// List events to get synthetic ids.
	from := start.Add(-time.Hour)
	to := start.Add(7 * 24 * time.Hour)
	q := "?from=" + from.Format(time.RFC3339) + "&to=" + to.Format(time.RFC3339)
	rr = authed(http.MethodGet, "/api/v1/events"+q, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var list struct {
		Events []struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			Recurrence string `json:"recurrence,omitempty"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))
	require.GreaterOrEqual(t, len(list.Events), 2, "need at least 2 occurrences")
	secondID := list.Events[1].ID
	assert.Contains(t, secondID, "::", "occurrence id must be synthetic")

	// (a) PATCH synthetic occurrence id → 200, master title updated.
	rr = authed(http.MethodPatch, "/api/v1/events/"+secondID, map[string]any{
		"title": "Daily Renamed",
	})
	require.Equal(t, http.StatusOK, rr.Code, "PATCH synthetic id must succeed, got %d: %s", rr.Code, rr.Body.String())

	// Verify master row reflects the change.
	rr = authed(http.MethodGet, "/api/v1/events/"+secondID, nil)
	require.Equal(t, http.StatusOK, rr.Code, "GET synthetic id must succeed, got %d: %s", rr.Code, rr.Body.String())
	var fetched struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &fetched))
	assert.Equal(t, "Daily Renamed", fetched.Title, "master title must be updated via synthetic id")

	// (b) GET synthetic occurrence id → 200; returned id is the master
	// (the ::N suffix is stripped, as designed).
	masterUUID := strings.Split(secondID, "::")[0]
	assert.Equal(t, masterUUID, fetched.ID, "GET must resolve to the master event id")

	// (c) PATCH unknown plain uuid → 404 (not 500).
	rr = authed(http.MethodPatch, "/api/v1/events/00000000-0000-0000-0000-000000000000", map[string]any{
		"title": "Nope",
	})
	assert.Equal(t, http.StatusNotFound, rr.Code, "unknown event id must 404, got %d: %s", rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "not_found")
}
