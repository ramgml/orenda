package api_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPhase11_PatchProject_ClearsDescription exercises the new
// pointer-field semantics: a JSON body with `"description": ""` must
// actually overwrite the existing description (not skip it like the
// old non-pointer version did).
func TestPhase11_PatchProject_ClearsDescription(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	projectID, _ := p3SeedProject(t, router, cookie, "P11-desc")

	type projResp struct {
		Description string `json:"description"`
	}

	// First set a description.
	rr := p3AuthJSON(router, http.MethodPatch, "/api/v1/projects/"+projectID, cookie, map[string]any{
		"description": "Initial copy.",
	})
	require.Equal(t, http.StatusOK, rr.Code, "seed body=%s", rr.Body.String())

	// Verify it's stored.
	rr = p3AuthGet(router, cookie, "/api/v1/projects/"+projectID)
	require.Equal(t, http.StatusOK, rr.Code)
	first := projResp{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &first))
	assert.Equal(t, "Initial copy.", first.Description)

	// Now PATCH with empty string — should clear.
	rr = p3AuthJSON(router, http.MethodPatch, "/api/v1/projects/"+projectID, cookie, map[string]any{
		"description": "",
	})
	require.Equal(t, http.StatusOK, rr.Code, "clear body=%s", rr.Body.String())

	rr = p3AuthGet(router, cookie, "/api/v1/projects/"+projectID)
	require.Equal(t, http.StatusOK, rr.Code)
	second := projResp{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &second))
	assert.Empty(t, second.Description, "description must be cleared, not preserved (got=%q)", second.Description)
}

// TestPhase11_PatchProject_RejectsEmptyName makes sure the rename
// path cannot erase a project's name (Phase 11.1 inline-edit UX:
// pressing Enter on an empty field is a no-op client-side, but the
// server is the last line of defence).
func TestPhase11_PatchProject_RejectsEmptyName(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	projectID, _ := p3SeedProject(t, router, cookie, "P11-name")

	rr := p3AuthJSON(router, http.MethodPatch, "/api/v1/projects/"+projectID, cookie, map[string]any{
		"name": "",
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code, "body=%s", rr.Body.String())

	// Name must still be the original.
	rr = p3AuthGet(router, cookie, "/api/v1/projects/"+projectID)
	require.Equal(t, http.StatusOK, rr.Code)
	var got struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "P11-name", got.Name)
}

// TestPhase11_PatchProject_ColorFallbackToDefault covers the
// "explicit empty color resets to default" semantics.
func TestPhase11_PatchProject_ColorFallbackToDefault(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	projectID, _ := p3SeedProject(t, router, cookie, "P11-color")

	type projResp struct {
		Color string `json:"color"`
	}

	rr := p3AuthJSON(router, http.MethodPatch, "/api/v1/projects/"+projectID, cookie, map[string]any{
		"color": "#ff00ff",
	})
	require.Equal(t, http.StatusOK, rr.Code)

	rr = p3AuthGet(router, cookie, "/api/v1/projects/"+projectID)
	require.Equal(t, http.StatusOK, rr.Code)
	first := projResp{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &first))
	assert.Equal(t, "#ff00ff", first.Color)

	// Now reset by sending an empty string.
	rr = p3AuthJSON(router, http.MethodPatch, "/api/v1/projects/"+projectID, cookie, map[string]any{
		"color": "",
	})
	require.Equal(t, http.StatusOK, rr.Code)
	rr = p3AuthGet(router, cookie, "/api/v1/projects/"+projectID)
	second := projResp{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &second))
	assert.Equal(t, "#3b82f6", second.Color, "empty color must fall back to DefaultColor")
}

// TestPhase11_ProjectActivity verifies the new GET
// /api/v1/projects/{id}/activity endpoint aggregates activity across
// every task in the project, newest first, and includes the joined
// task title. We exercise the aggregator with moves (the only path
// that reliably writes task_activity in the test router today — the
// full recorder wiring lives in cmd/orenda/main.go).
func TestPhase11_ProjectActivity(t *testing.T) {
	router, db := buildP3Router(t)
	cookie := p3Login(t, router)
	projectID, col := p3SeedProject(t, router, cookie, "P11-act")

	// Need two columns to move between. Create a second column
	// directly via SQL since the API doesn't expose column-create yet.
	secondCol := seedColumnForTest(t, db, projectID, "todo-2")

	// Seed two tasks; move each to the second column to log two
	// ActionMoved rows attributed to this project.
	taskA := p3SeedTask(t, router, cookie, projectID, col, "alpha")
	rr := p3AuthJSON(router, http.MethodPost, "/api/v1/tasks/"+taskA+"/move", cookie,
		map[string]any{"column_id": secondCol, "position": 1.0})
	require.Equal(t, http.StatusOK, rr.Code, "move A body=%s", rr.Body.String())

	taskB := p3SeedTask(t, router, cookie, projectID, col, "beta")
	rr = p3AuthJSON(router, http.MethodPost, "/api/v1/tasks/"+taskB+"/move", cookie,
		map[string]any{"column_id": secondCol, "position": 2.0})
	require.Equal(t, http.StatusOK, rr.Code, "move B body=%s", rr.Body.String())

	// Add a task in a *different* project; move it too — must NOT leak.
	otherProj, otherCol := p3SeedProject(t, router, cookie, "Other")
	otherTask := p3SeedTask(t, router, cookie, otherProj, otherCol, "ignored")
	otherSecondCol := seedColumnForTest(t, db, otherProj, "todo-2")
	rr = p3AuthJSON(router, http.MethodPost, "/api/v1/tasks/"+otherTask+"/move", cookie,
		map[string]any{"column_id": otherSecondCol, "position": 1.0})
	require.Equal(t, http.StatusOK, rr.Code)

	rr = p3AuthGet(router, cookie, "/api/v1/projects/"+projectID+"/activity")
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var resp struct {
		Activity []struct {
			TaskID    string `json:"task_id"`
			Action    string `json:"action"`
			TaskTitle string `json:"task_title"`
		} `json:"activity"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

	// Exactly two events for this project — one per move.
	require.Equal(t, 2, len(resp.Activity), "want 2 moved events in target project, got %d", len(resp.Activity))

	// Both events should be present, with their task titles, and the
	// ordering should be newest-first. We don't assert a strict
	// index-to-task mapping because both moves can land on the same
	// second (datetime('now') in SQLite) — instead we check the set
	// of (task_id, title) pairs and the action.
	got := map[string]string{} // task_id → task_title
	for _, ev := range resp.Activity {
		assert.Equal(t, "moved", ev.Action)
		got[ev.TaskID] = ev.TaskTitle
	}
	assert.Equal(t, "alpha", got[taskA], "missing alpha event")
	assert.Equal(t, "beta", got[taskB], "missing beta event")

	// No event from the other project must show up here.
	for _, ev := range resp.Activity {
		assert.NotEqual(t, "ignored", ev.TaskTitle, "leaked activity from another project")
		assert.NotEqual(t, otherTask, ev.TaskID, "leaked activity id from another project")
	}
}

// seedColumnForTest inserts a second column directly via SQL so the
// Phase 11 activity test can move tasks between columns. The test
// router does not expose column creation via the API yet.
func seedColumnForTest(t *testing.T, db *sqlLite, projectID, name string) string {
	t.Helper()
	// Find the board for this project.
	var boardID string
	require.NoError(t, db.QueryRow(
		`SELECT id FROM boards WHERE project_id = ? ORDER BY position ASC LIMIT 1`,
		projectID,
	).Scan(&boardID))
	colID := "col-" + uuidLite()
	_, err := db.Exec(
		`INSERT INTO columns (id, board_id, name, position) VALUES (?, ?, ?, ?)`,
		colID, boardID, name, 99.0,
	)
	require.NoError(t, err)
	return colID
}

// uuidLite returns a fresh hex string — used by Phase 11 tests that
// need collision-free IDs (randLite from phase3_handlers_test.go is
// deterministic per-test-name and collides under parallel runs).
func uuidLite() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// TestPhase11_ProjectAttachments covers the upload + list + download
// flow for the new project-scoped attachments tab.
func TestPhase11_ProjectAttachments(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	projectID, _ := p3SeedProject(t, router, cookie, "P11-files")

	// Upload one attachment against the project.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="notes.txt"`)
	hdr.Set("Content-Type", "text/plain")
	file, _ := mw.CreatePart(hdr)
	_, _ = file.Write([]byte("notes from project tab"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/projects/"+projectID+"/attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "upload body=%s", rr.Body.String())

	var up struct {
		ID         string `json:"id"`
		TargetType string `json:"target_type"`
		TargetID   string `json:"target_id"`
		Filename   string `json:"filename"`
		Mime       string `json:"mime"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &up))
	assert.Equal(t, "project", up.TargetType, "TargetType must be 'project'")
	assert.Equal(t, projectID, up.TargetID)
	assert.Equal(t, "notes.txt", up.Filename)
	assert.Equal(t, "text/plain", up.Mime)

	// List.
	rr = p3AuthGet(router, cookie, "/api/v1/projects/"+projectID+"/attachments")
	require.Equal(t, http.StatusOK, rr.Code)
	var list struct {
		Attachments []struct {
			ID         string `json:"id"`
			TargetType string `json:"target_type"`
			Filename   string `json:"filename"`
		} `json:"attachments"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))
	require.Len(t, list.Attachments, 1)
	assert.Equal(t, "project", list.Attachments[0].TargetType)
	assert.Equal(t, "notes.txt", list.Attachments[0].Filename)

	// Download via the global /api/v1/attachments/{id}/download route.
	rr = p3AuthGet(router, cookie, "/api/v1/attachments/"+up.ID+"/download")
	require.Equal(t, http.StatusOK, rr.Code, "download body=%s", rr.Body.String())
	assert.Equal(t, "notes from project tab", rr.Body.String())
	assert.Equal(t, "text/plain", rr.Header().Get("Content-Type"))
}

// TestPhase11_ProjectAttachments_NotFromOtherProjects ensures the
// project-scoped list does not leak attachments uploaded to a
// different project.
func TestPhase11_ProjectAttachments_NotFromOtherProjects(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	pA, _ := p3SeedProject(t, router, cookie, "Iso-A")
	pB, _ := p3SeedProject(t, router, cookie, "Iso-B")

	upload := func(projectID string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		hdr := make(textproto.MIMEHeader)
		hdr.Set("Content-Disposition", `form-data; name="file"; filename="x.txt"`)
		hdr.Set("Content-Type", "text/plain")
		f, _ := mw.CreatePart(hdr)
		_, _ = f.Write([]byte("x"))
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/projects/"+projectID+"/attachments", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		require.Equal(t, http.StatusCreated, rr.Code)
	}

	upload(pA)
	upload(pB)

	// List A — should only see A's file.
	rr := p3AuthGet(router, cookie, "/api/v1/projects/"+pA+"/attachments")
	require.Equal(t, http.StatusOK, rr.Code)
	var listA struct {
		Attachments []struct {
			TargetID string `json:"target_id"`
		} `json:"attachments"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &listA))
	for _, a := range listA.Attachments {
		assert.Equal(t, pA, a.TargetID, "leaked attachment from project B")
	}
}
