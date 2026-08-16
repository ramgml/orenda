// Package api — Phase 24: stats endpoint and slow-request log.
//
// Two pieces:
//
//  1. GET /api/v1/stats — small JSON snapshot the owner can hit from
//     any monitoring tool (curl, healthchecks, a tiny dashboard).
//     Includes uptime, WS connections, request counters, db file size,
//     last backup timestamp. No external dependencies (no Prometheus
//     client); the counters live in-process and reset on restart.
//
//  2. Slow-request logging: existing requestLogger already records
//     every request with the duration. Phase 24 turns durations over
//     500ms into a separate zap.Warn so a slow query is visible in
//     the log without grepping for big numbers in the info stream.
package api

import (
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/ramgml/orenda/internal/api/ws"
)

// stats is the in-process counter bundle. Accessed via atomic ops so
// the request path stays lock-free.
type stats struct {
	startedAt             time.Time
	totalReq              atomic.Uint64
	byStatus              [6]atomic.Uint64 // buckets: 2xx, 3xx, 4xx, 5xx, other
	slowCount             atomic.Uint64
	syncOpsRecordFailures atomic.Uint64 // Phase 30.2: sync_ops table write failures
}

var liveStats = &stats{startedAt: time.Now()}

// recordStats updates the per-request counters in a lock-free way.
//
// Status is bucketed:
//   - 2xx → index 0
//   - 3xx → index 1
//   - 4xx → index 2
//   - 5xx → index 3
//   - other (1xx) → index 4
//   - 0 (no response written) → index 5
func (s *stats) recordStats(status int, slow bool) {
	s.totalReq.Add(1)
	idx := 5
	switch {
	case status >= 200 && status < 300:
		idx = 0
	case status >= 300 && status < 400:
		idx = 1
	case status >= 400 && status < 500:
		idx = 2
	case status >= 500 && status < 600:
		idx = 3
	case status >= 100 && status < 200:
		idx = 4
	}
	s.byStatus[idx].Add(1)
	if slow {
		s.slowCount.Add(1)
	}
}

// statsResponse is the wire shape. We deliberately keep it small —
// the dashboard needs at-a-glance, not a full Prometheus exposition.
type statsResponse struct {
	UptimeSeconds         int64  `json:"uptime_seconds"`
	RequestsTotal         uint64 `json:"requests_total"`
	Requests2xx           uint64 `json:"requests_2xx"`
	Requests3xx           uint64 `json:"requests_3xx"`
	Requests4xx           uint64 `json:"requests_4xx"`
	Requests5xx           uint64 `json:"requests_5xx"`
	SlowRequests          uint64 `json:"slow_requests"`
	WSConnections         int    `json:"ws_connections"`
	DBBytes               int64  `json:"db_bytes"`
	DBPath                string `json:"db_path"`
	LastBackupUnix        int64  `json:"last_backup_unix,omitempty"`
	SyncOpsRecordFailures uint64 `json:"sync_ops_record_failures"` // Phase 30.2
}

// getStatsHandler returns the snapshot. Public — no auth required,
// the only sensitive bit (DB size) is harmless. If we ever decide to
// gate this, the auth middleware already exists.
func getStatsHandler(hub ws.Hub, dbPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := statsResponse{
			UptimeSeconds:         int64(time.Since(liveStats.startedAt).Seconds()),
			RequestsTotal:         liveStats.totalReq.Load(),
			Requests2xx:           liveStats.byStatus[0].Load(),
			Requests3xx:           liveStats.byStatus[1].Load(),
			Requests4xx:           liveStats.byStatus[2].Load(),
			Requests5xx:           liveStats.byStatus[3].Load(),
			SlowRequests:          liveStats.slowCount.Load(),
			DBPath:                dbPath,
			SyncOpsRecordFailures: liveStats.syncOpsRecordFailures.Load(),
		}
		if info, ok := hub.(hubStats); ok {
			resp.WSConnections = info.SubscriberCount()
		}
		if dbPath != "" {
			if fi, err := os.Stat(dbPath); err == nil {
				resp.DBBytes = fi.Size()
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// hubStats is the optional interface a Hub may implement to expose
// connection counts. We use it here so the ws package stays free of
// stats dependencies.
type hubStats interface {
	SubscriberCount() int
}
