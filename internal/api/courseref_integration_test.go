package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentCourses_CRefResolution: agent activate with C<N> ref resolves
// correctly, unknown C-ref returns 404 with explicit ref name.
func TestAgentCourses_CRefResolution(t *testing.T) {
	fx := newAgentCourseFixture(t)

	// Create a course via agent endpoint.
	body := map[string]string{"title": "Rust Ref Test"}
	rr := fx.agentReq(http.MethodPost, "/api/v1/agent/courses", body)
	require.Equal(t, http.StatusCreated, rr.Code, "create: %s", rr.Body.String())
	var created map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&created))
	courseID := created["id"].(string)

	// Submit curriculum so we can activate.
	curriculum := map[string]any{
		"modules": []map[string]any{
			{
				"id":          "m1",
				"title":       "Intro",
				"position":    0,
				"description": "",
				"lessons": []map[string]any{
					{
						"id":       "l1",
						"title":    "First",
						"position": 0,
						"status":   "locked",
					},
				},
			},
		},
	}
	rr = fx.agentReq(http.MethodPut, "/api/v1/agent/courses/"+courseID+"/curriculum", curriculum)
	require.Equal(t, http.StatusOK, rr.Code, "submit curriculum: %s", rr.Body.String())

	// Now get the course number from the repo to construct C<N>.
	c, err := fx.courses.GetCourse(t.Context(), courseID)
	require.NoError(t, err)
	require.NotZero(t, c.Number)

	// Approve with C<N> ref (user-side endpoint).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/courses/C"+itoa(c.Number)+"/approve", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr2 := httptest.NewRecorder()
	fx.router.ServeHTTP(rr2, req)
	require.Equal(t, http.StatusOK, rr2.Code, "approve C<N>: %s", rr2.Body.String())

	// Unknown C-ref returns 404 with explicit name.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/courses/C999999/approve", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr3 := httptest.NewRecorder()
	fx.router.ServeHTTP(rr3, req)
	require.Equal(t, http.StatusNotFound, rr3.Code, "unknown C-ref: %s", rr3.Body.String())
	var errBody map[string]string
	require.NoError(t, json.NewDecoder(rr3.Body).Decode(&errBody))
	assert.Equal(t, "course C999999 not found", errBody["error"])
}

// TestAgentCourses_LRefResolution: agent materialize with L<N> ref resolves
// correctly, unknown L-ref returns 404 with explicit ref name.
func TestAgentCourses_LRefResolution(t *testing.T) {
	fx := newAgentCourseFixture(t)

	// Create and activate a course with a lesson.
	body := map[string]string{"title": "L-Ref Test"}
	rr := fx.agentReq(http.MethodPost, "/api/v1/agent/courses", body)
	require.Equal(t, http.StatusCreated, rr.Code, "create: %s", rr.Body.String())
	var created map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&created))
	courseID := created["id"].(string)

	curriculum := map[string]any{
		"modules": []map[string]any{
			{
				"id":       "m1",
				"title":    "M1",
				"position": 0,
				"lessons": []map[string]any{
					{
						"id":       "l1",
						"title":    "Lesson One",
						"position": 0,
						"status":   "locked",
					},
				},
			},
		},
	}
	rr = fx.agentReq(http.MethodPut, "/api/v1/agent/courses/"+courseID+"/curriculum", curriculum)
	require.Equal(t, http.StatusOK, rr.Code, "submit curriculum: %s", rr.Body.String())

	// Approve the course (user-side).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/courses/"+courseID+"/approve", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr2 := httptest.NewRecorder()
	fx.router.ServeHTTP(rr2, req)
	require.Equal(t, http.StatusOK, rr2.Code, "approve: %s", rr2.Body.String())

	// Get the lesson to find its number.
	lessons, err := fx.courses.ListLessonsInCourse(t.Context(), courseID)
	require.NoError(t, err)
	require.Len(t, lessons, 1)
	require.NotZero(t, lessons[0].Number)

	// Materialize with L<N> ref.
	matBody := map[string]string{
		"content_md": "# Hello\nLesson content here.",
	}
	rr = fx.agentReq(http.MethodPost, "/api/v1/agent/lessons/L"+itoa(lessons[0].Number)+"/materialize", matBody)
	require.Equal(t, http.StatusOK, rr.Code, "materialize L<N>: %s", rr.Body.String())

	// Unknown L-ref returns 404 with explicit name.
	rr = fx.agentReq(http.MethodPost, "/api/v1/agent/lessons/L999999/materialize", matBody)
	require.Equal(t, http.StatusNotFound, rr.Code, "unknown L-ref: %s", rr.Body.String())
	var errBody map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&errBody))
	assert.Equal(t, "lesson L999999 not found", errBody["error"])
}

// TestUserCourses_CRefResolution: user getCourse with C<N> ref.
func TestUserCourses_CRefResolution(t *testing.T) {
	fx := newAgentCourseFixture(t)

	// Create a course via agent endpoint.
	body := map[string]string{"title": "User C-Ref Test"}
	rr := fx.agentReq(http.MethodPost, "/api/v1/agent/courses", body)
	require.Equal(t, http.StatusCreated, rr.Code)
	var created map[string]any
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&created))
	courseID := created["id"].(string)

	// Get course number.
	c, err := fx.courses.GetCourse(t.Context(), courseID)
	require.NoError(t, err)
	require.NotZero(t, c.Number)

	// User-side get with C<N> ref.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/courses/C"+itoa(c.Number), nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr2 := httptest.NewRecorder()
	fx.router.ServeHTTP(rr2, req)
	require.Equal(t, http.StatusOK, rr2.Code, "get C<N>: %s", rr2.Body.String())

	var tree map[string]any
	require.NoError(t, json.NewDecoder(rr2.Body).Decode(&tree))
	courseData := tree["course"].(map[string]any)
	assert.Equal(t, courseID, courseData["id"])

	// Unknown C-ref returns 404.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/courses/C999999", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	rr3 := httptest.NewRecorder()
	fx.router.ServeHTTP(rr3, req)
	assert.Equal(t, http.StatusNotFound, rr3.Code)
}

// itoa is a tiny int-to-string helper to avoid importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
