package api_test

// T16: dialog tutor API tests.
//
// Pins the wire contract of the four endpoints plus the WS fan-out:
//
//   - GET  /api/v1/lessons/{id}/tutor — history replay; unknown
//     lesson 404; empty thread 200 + [].
//   - POST /api/v1/lessons/{id}/tutor — 201 / 400 empty / 404
//     unknown lesson / 409 when a question is already pending.
//   - GET  /api/v1/agent/tutor/pending — the question comes back
//     with the lesson context (content_md + quizzes) embedded.
//   - POST /api/v1/agent/tutor/{lesson_id}/reply — 201 / 400 /
//     404 / 409 on a second reply without a new question.
//   - 401: a user session cookie must NOT open agent routes
//     (Bearer-only semantics); no credential must not open user
//     routes.
//   - WS: both POSTs emit on the "tutor" topic.
//
// The fixture mirrors newAgentCourseFixture but wires the tutor
// service (the pending/reply endpoints need it).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
	tutorsvc "github.com/ramgml/orenda/internal/service/tutor"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

type tutorFixture struct {
	router  http.Handler
	token   string // agent bearer
	cookie  string // owner session
	ownerID string // courses.owner_id FK target
	db      *sql.DB
	courses course.Repository
	hub     ws.Hub
}

func newTutorFixture(t *testing.T) *tutorFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	ownerEmail := "tutor-owner-" + randLite()[:8] + "@x.com"
	owner := &user.User{
		Email:        ownerEmail,
		PasswordHash: mustHashFast(t),
		DisplayName:  "Owner",
	}
	require.NoError(t, users.Create(context.Background(), owner))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	tokens := sqlite.NewAPITokenRepository(db)
	agents := sqlite.NewAgentRepository(db)
	agentSvc := agentservice.New(agents, users, &agentFixtureTMinter{tokens: tokens}, hub, nil)
	reg, err := agentSvc.Register(context.Background(), "tutor-test", []string{"tutor"}, "test", nil)
	require.NoError(t, err)

	coursesRepo := sqlite.NewCourseRepository(db)
	courseSvc := coursesvc.New(coursesRepo)
	tutorSvc := tutorsvc.New(sqlite.NewTutorMessageRepository(db), coursesRepo)
	courseActivityRecorder := coursesvc.NewCourseActivityRecorder(sqlite.NewCourseActivityRepository(db))
	// Mirror main.go's identitySourceFromAPI: map the api.Identity
	// onto the recorder's (actor type, id, ok) triple.
	courseActivityRecorder.IdentitySource = func(ctx context.Context) (course.ActorType, string, bool) {
		id, present := api.IdentityFrom(ctx)
		if !present {
			return "", "", false
		}
		if id.AgentID != "" {
			return course.ActorAgent, id.AgentID, true
		}
		return course.ActorUser, id.UserID, true
	}

	deps := api.Dependencies{
		Logger:                 zap.NewNop(),
		Signer:                 signer,
		Users:                  users,
		Tokens:                 tokens,
		Agents:                 agents,
		AgentService:           agentSvc,
		Courses:                coursesRepo,
		CourseService:          courseSvc,
		Tutor:                  tutorSvc,
		CourseActivityRecorder: courseActivityRecorder,
		WSHub:                  hub,
		CookieName:             "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	// Log the owner in for the user-side routes.
	body, _ := json.Marshal(map[string]string{"email": ownerEmail, "password": "hunter2!"})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)
	require.Equal(t, http.StatusOK, loginRR.Code, "login: %s", loginRR.Body.String())
	cookie := ""
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == "orenda_session" {
			cookie = c.Value
		}
	}
	require.NotEmpty(t, cookie)

	return &tutorFixture{router: router, token: reg.PlainToken, cookie: cookie,
		ownerID: owner.ID, db: db, courses: coursesRepo, hub: hub}
}

// seedOpenLesson creates course → module → open lesson through the
// domain repo (the tutor endpoints accept any existing lesson).
func (fx *tutorFixture) seedOpenLesson(t *testing.T, title string) *course.Lesson {
	t.Helper()
	ctx := context.Background()
	c := &course.Course{Title: "Course " + title, Status: course.StatusActive, OwnerID: fx.ownerID}
	require.NoError(t, fx.courses.CreateCourse(ctx, c))
	m := &course.Module{CourseID: c.ID, Title: "Module " + title, Position: 0}
	require.NoError(t, fx.courses.CreateModule(ctx, m))
	l := &course.Lesson{ModuleID: m.ID, Title: title, Status: course.LessonOpen, Position: 0, ContentMD: "# " + title}
	require.NoError(t, fx.courses.CreateLesson(ctx, l))
	return l
}

func (fx *tutorFixture) userReq(method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Cookie", "orenda_session="+fx.cookie)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr
}

func (fx *tutorFixture) agentReq(method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr
}

func TestTutor_AskHistoryReply_Lifecycle(t *testing.T) {
	t.Parallel()
	fx := newTutorFixture(t)
	lesson := fx.seedOpenLesson(t, "Lifecycle")

	// Empty thread: 200 + empty list (the lesson exists).
	rr := fx.userReq(http.MethodGet, fmt.Sprintf("/api/v1/lessons/%s/tutor", lesson.ID), nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var hist struct {
		Messages []*tutorsvc.Message `json:"messages"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&hist))
	require.NotNil(t, hist.Messages)
	assert.Empty(t, hist.Messages)

	// Ask: 201, role user.
	rr = fx.userReq(http.MethodPost, fmt.Sprintf("/api/v1/lessons/%s/tutor", lesson.ID), map[string]string{
		"question_md": "What is a rune?",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var q tutorsvc.Message
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&q))
	assert.Equal(t, "user", string(q.Role))
	assert.Equal(t, lesson.ID, q.LessonID)

	// Second ask while pending: 409.
	rr = fx.userReq(http.MethodPost, fmt.Sprintf("/api/v1/lessons/%s/tutor", lesson.ID), map[string]string{
		"question_md": "And a second one?",
	})
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())

	// Empty body: 400.
	rr = fx.userReq(http.MethodPost, fmt.Sprintf("/api/v1/lessons/%s/tutor", lesson.ID), map[string]string{
		"question_md": "   ",
	})
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())

	// Unknown lesson: 404 (both routes).
	rr = fx.userReq(http.MethodGet, "/api/v1/lessons/does-not-exist/tutor", nil)
	require.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())
	rr = fx.userReq(http.MethodPost, "/api/v1/lessons/does-not-exist/tutor", map[string]string{"question_md": "hi"})
	require.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())

	// Pending feed: the question with lesson context embedded.
	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/tutor/pending", nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var pend struct {
		Pending []*tutorsvc.PendingQuestion `json:"pending"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&pend))
	require.Len(t, pend.Pending, 1)
	pq := pend.Pending[0]
	assert.Equal(t, lesson.ID, pq.LessonID)
	assert.Equal(t, "What is a rune?", pq.BodyMD)
	assert.Equal(t, "Lifecycle", pq.LessonTitle)
	assert.Equal(t, "# Lifecycle", pq.ContentMD)
	assert.NotEmpty(t, pq.CourseID)

	// Reply: 201, role agent.
	rr = fx.agentReq(http.MethodPost, fmt.Sprintf("/api/v1/agent/tutor/%s/reply", lesson.ID), map[string]string{
		"body_md": "A rune is a code point.",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var reply tutorsvc.Message
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&reply))
	assert.Equal(t, "agent", string(reply.Role))
	assert.Equal(t, q.UserID, reply.UserID)

	// Second reply without a new question: 409.
	rr = fx.agentReq(http.MethodPost, fmt.Sprintf("/api/v1/agent/tutor/%s/reply", lesson.ID), map[string]string{
		"body_md": "Again?",
	})
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())

	// Reply to an unknown lesson: 404.
	rr = fx.agentReq(http.MethodPost, "/api/v1/agent/tutor/does-not-exist/reply", map[string]string{"body_md": "x"})
	require.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())

	// Empty reply: 400.
	rr = fx.agentReq(http.MethodPost, fmt.Sprintf("/api/v1/agent/tutor/%s/reply", lesson.ID), map[string]string{"body_md": ""})
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())

	// History now carries both turns, oldest first; the thread is
	// no longer pending.
	rr = fx.userReq(http.MethodGet, fmt.Sprintf("/api/v1/lessons/%s/tutor", lesson.ID), nil)
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&hist))
	require.Len(t, hist.Messages, 2)
	assert.Equal(t, "user", string(hist.Messages[0].Role))
	assert.Equal(t, "agent", string(hist.Messages[1].Role))

	rr = fx.agentReq(http.MethodGet, "/api/v1/agent/tutor/pending", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&pend))
	assert.Empty(t, pend.Pending)
}

// TestTutor_AgentRoutesRejectUserCookie pins the DoD security
// requirement: the agent endpoints are Bearer-only — a valid user
// session cookie must yield 401 there (RequireAgent never consults
// cookies), and the user routes must not accept no credentials.
func TestTutor_AgentRoutesRejectUserCookie(t *testing.T) {
	t.Parallel()
	fx := newTutorFixture(t)
	lesson := fx.seedOpenLesson(t, "CookieGate")

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/agent/tutor/pending"},
		{http.MethodPost, fmt.Sprintf("/api/v1/agent/tutor/%s/reply", lesson.ID)},
	} {
		var buf bytes.Buffer
		_ = json.NewEncoder(&buf).Encode(map[string]string{"body_md": "x"})
		req := httptest.NewRequest(tc.method, tc.path, &buf)
		req.Header.Set("Cookie", "orenda_session="+fx.cookie) // no Authorization header
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code, "%s %s: %s", tc.method, tc.path, rr.Body.String())
	}

	// And without any credential the user routes are 401 too.
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/lessons/%s/tutor", lesson.ID), nil)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// TestTutor_WSEmit pins the "tutor" topic fan-out on both POSTs.
// The hub filters by user_id, so subscribing as the owner (the
// cookie user who asks, and the user the reply is addressed to)
// receives both sides of the dialog.
func TestTutor_WSEmit(t *testing.T) {
	t.Parallel()
	fx := newTutorFixture(t)
	lesson := fx.seedOpenLesson(t, "WSEmit")

	ch, unsub := fx.hub.Subscribe(fx.ownerID, "tutor")
	defer unsub()

	// User ask emits on "tutor".
	rr := fx.userReq(http.MethodPost, fmt.Sprintf("/api/v1/lessons/%s/tutor", lesson.ID), map[string]string{
		"question_md": "ws question",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	select {
	case ev := <-ch:
		assert.Equal(t, "tutor", ev.Topic)
		body, ok := ev.Body.(map[string]any)
		require.True(t, ok, "body must be a map, got %T", ev.Body)
		assert.Equal(t, fx.ownerID, body["user_id"])
		assert.Equal(t, lesson.ID, body["lesson_id"])
		assert.Equal(t, "user", body["role"])
	case <-time.After(2 * time.Second):
		t.Fatal("no tutor event after ask")
	}

	// Agent reply emits on "tutor" too.
	rr = fx.agentReq(http.MethodPost, fmt.Sprintf("/api/v1/agent/tutor/%s/reply", lesson.ID), map[string]string{
		"body_md": "ws answer",
	})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	select {
	case ev := <-ch:
		assert.Equal(t, "tutor", ev.Topic)
		body := ev.Body.(map[string]any)
		assert.Equal(t, "agent", body["role"])
	case <-time.After(2 * time.Second):
		t.Fatal("no tutor event after reply")
	}
}

// TestTutor_Isolation: another student's cookie cannot see this
// thread's history — the thread is (lesson, user).
func TestTutor_Isolation(t *testing.T) {
	t.Parallel()
	fx := newTutorFixture(t)
	lesson := fx.seedOpenLesson(t, "Isolation")

	rr := fx.userReq(http.MethodPost, fmt.Sprintf("/api/v1/lessons/%s/tutor", lesson.ID), map[string]string{
		"question_md": "owner question",
	})
	require.Equal(t, http.StatusCreated, rr.Code)

	// Second user on the same lesson: empty thread.
	users := sqlite.NewUserRepository(fx.db)
	second := &user.User{Email: "tutor-second-" + randLite()[:8] + "@x.com", PasswordHash: mustHashFast(t), DisplayName: "S"}
	require.NoError(t, users.Create(context.Background(), second))

	body, _ := json.Marshal(map[string]string{"email": second.Email, "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr2 := httptest.NewRecorder()
	fx.router.ServeHTTP(rr2, req)
	require.Equal(t, http.StatusOK, rr2.Code)
	cookie2 := ""
	for _, c := range rr2.Result().Cookies() {
		if c.Name == "orenda_session" {
			cookie2 = c.Value
		}
	}
	require.NotEmpty(t, cookie2)

	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/lessons/%s/tutor", lesson.ID), nil)
	req.Header.Set("Cookie", "orenda_session="+cookie2)
	rr3 := httptest.NewRecorder()
	fx.router.ServeHTTP(rr3, req)
	require.Equal(t, http.StatusOK, rr3.Code)
	var hist struct {
		Messages []*tutorsvc.Message `json:"messages"`
	}
	require.NoError(t, json.NewDecoder(rr3.Body).Decode(&hist))
	assert.Empty(t, hist.Messages, "second user must not see the owner's thread")
}
