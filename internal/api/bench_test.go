// Package api — benchmarks for the hot paths.
//
// Run with: go test -bench=. -benchmem ./internal/api/...
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
	"go.uber.org/zap"
)

// benchRouter builds a router with a pre-authenticated user.
func benchRouter(b *testing.B) (http.Handler, string) {
	b.Helper()
	dir := b.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/b.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	if err := sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"); err != nil {
		b.Fatal(err)
	}

	users := sqlite.NewUserRepository(db)
	hash, _ := auth.HashPassword("hunter2!", 4)
	if err := users.Create(context.Background(), &user.User{
		Email: "bench@x.com", PasswordHash: hash, DisplayName: "Bench",
	}); err != nil {
		b.Fatal(err)
	}

	hub := ws.NewHub()
	b.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	commentSvc := commentservice.New(sqlite.NewCommentRepository(db), hub, nil)
	taskSvc := taskservice.New(
		sqlite.NewTaskRepository(db),
		sqlite.NewTaskLockRepository(db),
		nil, nil, hub,
	)
	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    sqlite.NewProjectRepository(db),
		Tasks:       sqlite.NewTaskRepository(db),
		Tokens:      sqlite.NewAPITokenRepository(db),
		TaskService: taskSvc,
		Agents:      sqlite.NewAgentRepository(db),
		Comments:    commentSvc,
		Activities:  sqlite.NewActivityRepository(db),
		SyncOps:     sqlite.NewSyncOpsRepository(db),
		WSHub:       hub,
		CookieName:  "orenda_session",
	}
	router := api.NewRouter(deps)

	body, _ := json.Marshal(map[string]string{"email": "bench@x.com", "password": "hunter2!"})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return router, rr.Result().Cookies()[0].Value
}

func BenchmarkHealthz(b *testing.B) {
	router := api.NewRouter(api.Dependencies{Logger: zap.NewNop()})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/healthz", nil)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
	}
}

func BenchmarkMeAuthenticated(b *testing.B) {
	router, cookie := benchRouter(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/api/v1/me", nil)
		req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
	}
}

func BenchmarkListProjects(b *testing.B) {
	router, cookie := benchRouter(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/api/v1/projects", nil)
		req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
	}
}
