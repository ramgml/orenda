package api_test

// Phase 29.4 / 29.5: agent-namespace course creation + activation.
//
// The target scenario: "agent, build me a course on X" runs
// end-to-end without a single human click. These tests pin:
//
//   - POST /agent/courses creates a draft owned by the first
//     non-system user and does NOT spawn a generator task (the
//     agent is the generator — SkipGenerator is forced).
//   - POST /agent/courses/{id}/activate shares the approve path:
//     review → active, first lesson unlocked; draft → 422.
//   - 400 without a title; 404 for a missing course; 401 without
//     an agent token (user cookie is not an agent credential).

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// countingTaskCreator records TaskCreator calls so the tests can
// prove the agent path never spawns a generator task.
type countingTaskCreator struct {
	generatorCalls int
}

func (c *countingTaskCreator) CreateGeneratorTask(_ context.Context, _, _, _, _ string) (string, error) {
	c.generatorCalls++
	return "task-x", nil
}

func (c *countingTaskCreator) CreateQuizReviewTask(_ context.Context, _, _, _, _ string) (string, error) {
	return "task-y", nil
}

type agentCourseFixture struct {
	router  http.Handler
	token   string
	cookie  string
	ownerID string
	courses course.Repository
	creator *countingTaskCreator
}

func newAgentCourseFixture(t *testing.T) *agentCourseFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	ownerEmail := "ac-owner-" + randLite()[:8] + "@x.com"
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
	reg, err := agentSvc.Register(context.Background(), "course-agent", []string{"tutor"}, "test", nil)
	require.NoError(t, err)

	coursesRepo := sqlite.NewCourseRepository(db)
	creator := &countingTaskCreator{}
	courseSvc := coursesvc.New(coursesRepo).WithTaskCreator(creator)

	deps := api.Dependencies{
		Logger:        zap.NewNop(),
		Signer:        signer,
		Users:         users,
		Tokens:        tokens,
		Agents:        agents,
		AgentService:  agentSvc,
		Courses:       coursesRepo,
		CourseService: courseSvc,
		WSHub:         hub,
		CookieName:    "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	// Log the owner in for user-side cross-checks.
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

	return &agentCourseFixture{
		router:  router,
		token:   reg.PlainToken,
		cookie:  cookie,
		ownerID: owner.ID,
		courses: coursesRepo,
		creator: creator,
	}
}

func (fx *agentCourseFixture) agentReq(method, path string, body any) *httptest.ResponseRecorder {
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

func TestAgentCourses_CreateActivate_EndToEnd(t *testing.T) {
	t.Parallel()
	fx := newAgentCourseFixture(t)

	// Create: 201, draft, owned by the first non-system user, and
	// NO generator task (the agent is the generator).
	rr := fx.agentReq(http.MethodPost, "/api/v1/agent/courses", map[string]any{
		"title":     "Learn OpenCode",
		"intent_md": "30 minutes a day, beginner level",
	})
	require.Equal(t, http.StatusCreated, rr.Code, "create: %s", rr.Body.String())
	var c course.Course
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&c))
	assert.Equal(t, course.StatusDraft, c.Status)
	assert.Equal(t, fx.ownerID, c.OwnerID, "owner resolves to the first non-system user")
	assert.Empty(t, c.GeneratorTaskID, "agent-created course must not spawn a generator task")
	assert.Equal(t, 0, fx.creator.generatorCalls, "TaskCreator must not be invoked on the agent path")

	// Curriculum: one module, two lessons, one exact quiz.
	rr = fx.agentReq(http.MethodPut, "/api/v1/agent/courses/"+c.ID+"/curriculum", map[string]any{
		"modules": []map[string]any{
			{
				"title": "Basics", "position": 1,
				"lessons": []map[string]any{
					{"title": "Intro", "position": 1, "quizzes": []map[string]any{
						{"position": 1, "question_md": "2+2?", "expected_md": "4", "kind": "exact"},
					}},
					{"title": "Deep dive", "position": 2},
				},
			},
		},
	})
	require.Equal(t, http.StatusOK, rr.Code, "curriculum: %s", rr.Body.String())

	// Activate: review → active, first lesson unlocked.
	rr = fx.agentReq(http.MethodPost, "/api/v1/agent/courses/"+c.ID+"/activate", nil)
	require.Equal(t, http.StatusOK, rr.Code, "activate: %s", rr.Body.String())
	var active course.Course
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&active))
	assert.Equal(t, course.StatusActive, active.Status)

	lessons, err := fx.courses.ListLessonsInCourse(context.Background(), c.ID)
	require.NoError(t, err)
	require.Len(t, lessons, 2)
	assert.Equal(t, course.LessonOpen, lessons[0].Status, "activation unlocks the first lesson")
	assert.Equal(t, course.LessonLocked, lessons[1].Status)

	// The owner sees the course on the user side (single-owner).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/courses/"+c.ID, nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "user-side get: %s", rr.Body.String())
}

func TestAgentCourses_Create_MissingTitle(t *testing.T) {
	t.Parallel()
	fx := newAgentCourseFixture(t)
	rr := fx.agentReq(http.MethodPost, "/api/v1/agent/courses", map[string]any{
		"intent_md": "no title here",
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "missing_title")
}

func TestAgentCourses_Activate_InvalidTransition(t *testing.T) {
	t.Parallel()
	fx := newAgentCourseFixture(t)

	rr := fx.agentReq(http.MethodPost, "/api/v1/agent/courses", map[string]any{"title": "Drafty"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var c course.Course
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&c))

	// draft → activate is not a legal transition (must pass review).
	rr = fx.agentReq(http.MethodPost, "/api/v1/agent/courses/"+c.ID+"/activate", nil)
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code)
	assert.Contains(t, rr.Body.String(), "invalid_transition")
}

func TestAgentCourses_Activate_NotFound(t *testing.T) {
	t.Parallel()
	fx := newAgentCourseFixture(t)
	rr := fx.agentReq(http.MethodPost, "/api/v1/agent/courses/does-not-exist/activate", nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestAgentCourses_RequiresAgentToken(t *testing.T) {
	t.Parallel()
	fx := newAgentCourseFixture(t)

	// No token.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/courses", nil)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// User cookie is NOT an agent credential.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/courses", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
