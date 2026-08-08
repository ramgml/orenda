package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p8PostSync posts a batch of ops through the cookie-authenticated router.
func p8PostSync(t *testing.T, router http.Handler, cookie string, ops []map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"ops": ops})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sync", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

type syncResultRow struct {
	ClientID string `json:"client_id"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	ID       string `json:"id,omitempty"`
}

func parseSyncResults(t *testing.T, rr *httptest.ResponseRecorder) []syncResultRow {
	t.Helper()
	var resp struct {
		Results []syncResultRow `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	return resp.Results
}

func TestP8_Sync_CreateTask_Idempotent(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	projID, colID := p3SeedProject(t, router, cookie, "P8")

	op := map[string]any{
		"op":         "create_task",
		"target":     projID,
		"payload":    map[string]any{"title": "offline task", "column_id": colID},
		"client_id":  "c-test-1",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	rr := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	results := parseSyncResults(t, rr)
	require.Len(t, results, 1)
	assert.True(t, results[0].OK)
	assert.NotEmpty(t, results[0].ID)

	// Replay same client_id: no-op returning the same server id.
	rr2 := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr2.Code)
	results2 := parseSyncResults(t, rr2)
	require.Len(t, results2, 1)
	assert.True(t, results2[0].OK)
	assert.Equal(t, results[0].ID, results2[0].ID, "idempotent replay returns same id")
}

func TestP8_Sync_MoveTask(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	projID, colID := p3SeedProject(t, router, cookie, "P8m")
	taskID := p3SeedTask(t, router, cookie, projID, colID, "x")

	op := map[string]any{
		"op":         "move_task",
		"target":     taskID,
		"payload":    map[string]any{"column_id": colID},
		"client_id":  "c-move-1",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	rr := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr.Code)
	results := parseSyncResults(t, rr)
	require.Len(t, results, 1)
	assert.True(t, results[0].OK, "err=%s", results[0].Error)
}

func TestP8_Sync_UnsupportedOp(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)

	op := map[string]any{
		"op":         "explode",
		"target":     "x",
		"payload":    map[string]any{},
		"client_id":  "c-x",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	rr := p8PostSync(t, router, cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr.Code)
	results := parseSyncResults(t, rr)
	require.Len(t, results, 1)
	assert.False(t, results[0].OK)
	assert.Equal(t, "unsupported_op", results[0].Error)
}

func TestP8_Sync_EmptyBatch(t *testing.T) {
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	rr := p8PostSync(t, router, cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
}
