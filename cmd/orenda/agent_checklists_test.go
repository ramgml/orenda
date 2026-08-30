// T96: `orenda agent checklist*` CLI subcommands — wire-shape tests
// against an httptest backend (same pattern as the pages/search/propose
// CLI tests): method, path, body, auth header, and output handling.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentChecklists_ListsTaskChecklists(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"checklists":[{"id":"cl-1","title":"Как протестировать","position":0}],"checklist_items":{"cl-1":[{"id":"it-1","title":"make test","done":false}]}}`))
	}))
	t.Cleanup(srv.Close)

	out, err := runAgentCLI(t, srv, "checklists", "t-9")
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/api/v1/agent/tasks/t-9/checklists", gotPath)
	assert.Equal(t, "Bearer test-token", gotAuth)
	assert.Contains(t, out, "Как протестировать")
	assert.Contains(t, out, "make test")
}

func TestAgentChecklistAdd_PostsTitle(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"cl-2","title":"QA","task_id":"t-9"}`))
	}))
	t.Cleanup(srv.Close)

	out, err := runAgentCLI(t, srv, "checklist-add", "t-9", "QA")
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v1/agent/tasks/t-9/checklists", gotPath)
	assert.Equal(t, "QA", gotBody["title"])
	assert.Contains(t, out, `"cl-2"`)
}

func TestAgentChecklistItemAdd_PostsItem(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"it-2","title":"lint"}`))
	}))
	t.Cleanup(srv.Close)

	out, err := runAgentCLI(t, srv, "checklist-item-add", "t-9", "cl-2", "lint")
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v1/agent/tasks/t-9/checklists/cl-2/items", gotPath)
	assert.Equal(t, "lint", gotBody["title"])
	assert.Contains(t, out, `"it-2"`)
}

func TestAgentChecklistItemUpdate_SendsDoneFlag(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	out, err := runAgentCLI(t, srv, "checklist-item-update", "t-9", "cl-2", "it-2", "--done")
	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, gotMethod)
	assert.Equal(t, "/api/v1/agent/tasks/t-9/checklists/cl-2/items/it-2", gotPath)
	assert.Equal(t, true, gotBody["done"])
	assert.Contains(t, out, "updated")
}

func TestAgentChecklistItemUpdate_UntickAndTitle(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	// --done=false unticks; --title renames; both ride one PATCH.
	_, err := runAgentCLI(t, srv, "checklist-item-update", "t-9", "cl-2", "it-2",
		"--done=false", "--title", "new name")
	require.NoError(t, err)
	assert.Equal(t, false, gotBody["done"])
	assert.Equal(t, "new name", gotBody["title"])
}

func TestAgentChecklistItemUpdate_NoFlagsFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no PATCH may be sent when no flags are given")
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "checklist-item-update", "t-9", "cl-2", "it-2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to update")
}

func TestAgentChecklistItemDelete_SendsDelete(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	out, err := runAgentCLI(t, srv, "checklist-item-delete", "t-9", "cl-2", "it-2")
	require.NoError(t, err)
	assert.Equal(t, http.MethodDelete, gotMethod)
	assert.Equal(t, "/api/v1/agent/tasks/t-9/checklists/cl-2/items/it-2", gotPath)
	assert.Contains(t, out, "deleted")
}

func TestAgentChecklistAdd_ServerErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"not_lock_holder"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := runAgentCLI(t, srv, "checklist-add", "t-9", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Contains(t, err.Error(), "not_lock_holder")
}
