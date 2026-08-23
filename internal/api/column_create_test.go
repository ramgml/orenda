package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
)

func createColumnReq(router http.Handler, cookie, projectID string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID+"/columns", bytes.NewReader(raw))
	req.Header.Set("Cookie", "orenda_session="+cookie)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// 1) Happy path: create a column and confirm it appears in GET /board.
func TestCreateColumn_Success(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	rr := createColumnReq(f.router, f.cookie, f.projectID, map[string]any{
		"name":  "QA",
		"color": "#0ea5e9",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var out project.Column
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&out))
	assert.NotEmpty(t, out.ID)
	assert.Equal(t, "QA", out.Name)
	assert.Equal(t, "#0ea5e9", out.Color)
	assert.NotZero(t, out.Position, "repo should assign a non-zero position")

	// Board now has the default 5 + new "QA" = 6.
	rr = authedBoardReq(f.router, f.cookie, f.projectID)
	require.Equal(t, http.StatusOK, rr.Code)
	var board struct {
		Columns []project.Column `json:"columns"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&board))
	require.Len(t, board.Columns, 6)
	assert.Equal(t, "QA", board.Columns[5].Name)
	assert.Greater(t, board.Columns[5].Position, board.Columns[4].Position,
		"new column should land after existing ones")
}

// 2) wip_limit is honored on create.
func TestCreateColumn_WithWIPLimit(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	three := 3
	rr := createColumnReq(f.router, f.cookie, f.projectID, map[string]any{
		"name":      "QA",
		"wip_limit": &three,
	})
	require.Equal(t, http.StatusCreated, rr.Code)
	var out project.Column
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&out))
	require.NotNil(t, out.WIPLimit)
	assert.Equal(t, 3, *out.WIPLimit)
}

// 3) Empty name → 400.
func TestCreateColumn_EmptyName(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	rr := createColumnReq(f.router, f.cookie, f.projectID, map[string]any{"name": ""})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	body := rr.Body.String()
	assert.True(t, strings.Contains(body, "name_required"), "body=%s", body)
}

// 4) Missing body → 400.
func TestCreateColumn_InvalidJSON(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+f.projectID+"/columns",
		bytes.NewReader([]byte("{not-json")))
	req.Header.Set("Cookie", "orenda_session="+f.cookie)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// 5) Negative wip_limit → 400.
func TestCreateColumn_NegativeWIP(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	neg := -1
	rr := createColumnReq(f.router, f.cookie, f.projectID, map[string]any{
		"name":      "QA",
		"wip_limit": &neg,
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// 6) Unknown project → 404 (no board exists).
func TestCreateColumn_ProjectNotFound(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)
	rr := createColumnReq(f.router, f.cookie, "deadbeef-0000-0000-0000-000000000000",
		map[string]any{"name": "QA"})
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// 7) After create + reorder (via PATCH position), GET /board preserves the
// new order. This is the integration glue the kanban dnd code depends on.
func TestCreateColumn_ThenReorderByPosition(t *testing.T) {
	t.Parallel()
	f := columnDeps(t)

	// Insert a fresh column at the end (max+1024).
	rr := createColumnReq(f.router, f.cookie, f.projectID, map[string]any{"name": "QA"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var created project.Column
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&created))

	// Bump "QA" between cols[1] and cols[2]: new position is midpoint.
	mid := (f.cols[1].Position + f.cols[2].Position) / 2
	rr = patchColumn(f.router, f.cookie, created.ID, map[string]any{"position": mid})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	rr = authedBoardReq(f.router, f.cookie, f.projectID)
	require.Equal(t, http.StatusOK, rr.Code)
	var board struct {
		Columns []project.Column `json:"columns"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&board))
	// Find the new column — it should now sit at index 2 (between original [1] and [2]).
	var idx = -1
	for i, c := range board.Columns {
		if c.ID == created.ID {
			idx = i
			break
		}
	}
	require.NotEqual(t, -1, idx)
	assert.Equal(t, 2, idx, "QA should be at index 2 after midpoint reorder")
	assert.Equal(t, "QA", board.Columns[idx].Name)
	assert.Equal(t, f.cols[1].ID, board.Columns[idx-1].ID)
	assert.Equal(t, f.cols[2].ID, board.Columns[idx+1].ID)
}

// authedBoardReq is a tiny helper for create-column tests that need to
// fetch the board after mutations.
func authedBoardReq(router http.Handler, cookie, projectID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/board", nil)
	req.Header.Set("Cookie", "orenda_session="+cookie)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}
