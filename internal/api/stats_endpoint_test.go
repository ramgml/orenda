package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
)

// Phase 24: /api/v1/stats smoke + the slow-request threshold.
//
// We don't depend on a real DB — the handler is happy with an empty
// path (db_bytes=0, no panic). The fixture gives us a hub to read
// the subscriber count from.
func TestStats_Endpoint(t *testing.T) {
	f := columnDeps(t)

	rr := doReq(f.router, http.MethodGet, "/api/v1/stats", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var got struct {
		UptimeSeconds int64  `json:"uptime_seconds"`
		RequestsTotal uint64 `json:"requests_total"`
		Requests2xx   uint64 `json:"requests_2xx"`
		Requests4xx   uint64 `json:"requests_4xx"`
		WSConnections int    `json:"ws_connections"`
		DBBytes       int64  `json:"db_bytes"`
		DBPath        string `json:"db_path"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))

	// At least one request must have been recorded (the stats call
	// itself doesn't count before it returns, but the auth login +
	// various setup calls already did — counter is global).
	assert.GreaterOrEqual(t, got.RequestsTotal, uint64(1))
	// The stats call returns 2xx.
	assert.GreaterOrEqual(t, got.Requests2xx, uint64(1))
}

// The stats endpoint is public — no cookie required.
func TestStats_Public(t *testing.T) {
	f := columnDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

// SubscriberCount returns the right number for a hub with two
// subscribers on the same topic. (Stats endpoint picks this up.)
func TestHub_SubscriberCount(t *testing.T) {
	h := ws.NewHub()
	if closer, ok := h.(interface{ Close() }); ok {
		t.Cleanup(closer.Close)
	}
	// Type assertion — channelHub implements the optional interface.
	type counter interface {
		SubscriberCount() int
	}
	c, ok := h.(counter)
	require.True(t, ok, "channelHub should implement SubscriberCount")
	assert.Equal(t, 0, c.SubscriberCount())

	_, unsub1 := h.Subscribe("u1", "tasks")
	defer unsub1()
	_, unsub2 := h.Subscribe("u2", "tasks")
	defer unsub2()
	assert.Equal(t, 2, c.SubscriberCount())

	unsub1()
	assert.Equal(t, 1, c.SubscriberCount())
}
