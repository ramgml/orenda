// White-box tests for the maintenance middleware.
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaintenance_ToggleRoundTrip(t *testing.T) {
	t.Cleanup(func() { MaintenanceOff() })

	require.False(t, IsMaintenanceOn())

	// Build a tiny router with the middleware + a toggle pair.
	mux := chi.NewRouter()
	mux.Use(maintenanceMiddleware)
	mux.Post("/api/v1/maintenance/on", maintenanceToggleHandler("on"))
	mux.Post("/api/v1/maintenance/off", maintenanceToggleHandler("off"))

	// Toggle on.
	rr := doReqMaint(mux, "/api/v1/maintenance/on", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, true, got["maintenance"])
	assert.Equal(t, false, got["was_already"])
	assert.True(t, IsMaintenanceOn())

	// Toggle on again — was_already=true.
	rr = doReqMaint(mux, "/api/v1/maintenance/on", "")
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, true, got["was_already"])

	// Toggle off.
	rr = doReqMaint(mux, "/api/v1/maintenance/off", "")
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, false, got["maintenance"])
}

func TestMaintenance_BlocksWritesDuringMaintenance(t *testing.T) {
	t.Cleanup(func() { MaintenanceOff() })

	mux := chi.NewRouter()
	mux.Use(maintenanceMiddleware)
	mux.Post("/api/v1/maintenance/on", maintenanceToggleHandler("on"))
	mux.Post("/api/v1/maintenance/off", maintenanceToggleHandler("off"))
	mux.Post("/api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	mux.Get("/api/v1/info", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Enter maintenance.
	rr := doReqMaint(mux, "/api/v1/maintenance/on", "")
	require.Equal(t, http.StatusOK, rr.Code)

	// POST should 503.
	rr = doReqMaint(mux, "/api/v1/projects", "")
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.Contains(t, rr.Body.String(), "maintenance_mode")

	// GET still works.
	rr = doReqMaintGet(mux, "/api/v1/info", "")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestMaintenance_AllowsToggleDuringMaintenance(t *testing.T) {
	t.Cleanup(func() { MaintenanceOff() })

	mux := chi.NewRouter()
	mux.Use(maintenanceMiddleware)
	mux.Post("/api/v1/maintenance/on", maintenanceToggleHandler("on"))
	mux.Post("/api/v1/maintenance/off", maintenanceToggleHandler("off"))

	// Enter maintenance, then verify we can still toggle off.
	rr := doReqMaint(mux, "/api/v1/maintenance/on", "")
	require.Equal(t, http.StatusOK, rr.Code)
	rr = doReqMaint(mux, "/api/v1/maintenance/off", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.False(t, IsMaintenanceOn())
}

// doReqMaint is a tiny helper for the maintenance tests — no
// cookie, no body, just a path.
func doReqMaint(router http.Handler, path, _ string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// doReqMaintGet is the GET equivalent.
func doReqMaintGet(router http.Handler, path, _ string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}
