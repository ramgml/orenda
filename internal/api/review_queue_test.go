package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/task"
)

// reviewQueueResponse mirrors the wire shape of GET /review-queue.
type reviewQueueResponse struct {
	Tasks []task.ReviewQueueItem `json:"tasks"`
	Count int                    `json:"count"`
}

type reviewCountResponse struct {
	Count int `json:"count"`
}

// reviewFixture extends columnDeps with task-create access for seeding.
type reviewQueueFixture struct {
	colFixtures
}

// 1) Empty queue: 200 with empty tasks/count=0.
func TestReviewQueue_Empty(t *testing.T) {
	f := columnDeps(t)

	rr := doReq(f.router, http.MethodGet, "/api/v1/review-queue", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var got reviewQueueResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Empty(t, got.Tasks)
	assert.Equal(t, 0, got.Count)

	// /count endpoint returns the same shape but cheaper.
	rr = doReq(f.router, http.MethodGet, "/api/v1/review-queue/count", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var cnt reviewCountResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&cnt))
	assert.Equal(t, 0, cnt.Count)
}

// 2) Submit-style flow: agent submits a task, owner sees it in the queue;
// accept → task is done and gone from the queue; reject with comment →
// task returns to in_progress with awaiting=agent.
func TestReviewQueue_FullFlow(t *testing.T) {
	f := columnDeps(t)

	// Owner has to act as both the agent (submit) and the human (review)
	// in this test: we don't bootstrap an agent, just drive the service
	// directly through its repo.
	tr := &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[1].ID,
		Title: "needs review",
		// submit() on the service sets status=review and awaiting=human
		Status: task.StatusReview, Awaiting: task.AwaitingHuman,
	}
	require.NoError(t, f.tasks.Create(context.Background(), tr))

	// 1. Task is in the queue.
	rr := doReq(f.router, http.MethodGet, "/api/v1/review-queue", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var q1 reviewQueueResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&q1))
	require.Equal(t, 1, q1.Count, "submitted task must show in the queue")
	require.Len(t, q1.Tasks, 1)
	require.NotNil(t, q1.Tasks[0].Task)
	assert.Equal(t, tr.ID, q1.Tasks[0].Task.ID)
	assert.Equal(t, "Demo", q1.Tasks[0].ProjectName)
	assert.Equal(t, "#3b82f6", q1.Tasks[0].ProjectColor)

	// 2. Owner accepts → done, removed from queue.
	rr = doReq(f.router, http.MethodPost, "/api/v1/tasks/"+tr.ID+"/review", f.cookie, map[string]any{
		"decision": "approve",
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var accepted task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&accepted))
	assert.Equal(t, task.StatusDone, accepted.Status)
	assert.Equal(t, task.AwaitingNone, accepted.Awaiting)
	assert.NotNil(t, accepted.CompletedAt)

	rr = doReq(f.router, http.MethodGet, "/api/v1/review-queue", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var q2 reviewQueueResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&q2))
	assert.Equal(t, 0, q2.Count, "after accept, queue is empty")

	// 3. Owner rejects a different task with a comment → it goes back
	// to in_progress + awaiting=agent, and drops out of the queue.
	tr2 := &task.Task{
		ProjectID: f.projectID, ColumnID: f.cols[1].ID,
		Title:  "needs rework",
		Status: task.StatusReview, Awaiting: task.AwaitingHuman,
	}
	require.NoError(t, f.tasks.Create(context.Background(), tr2))

	// Confirm it's queued before reject.
	rr = doReq(f.router, http.MethodGet, "/api/v1/review-queue", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var qMid reviewQueueResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&qMid))
	assert.Equal(t, 1, qMid.Count)

	rr = doReq(f.router, http.MethodPost, "/api/v1/tasks/"+tr2.ID+"/review", f.cookie, map[string]any{
		"decision": "reject",
		"comment":  "Wrong colour scheme — try slate.",
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var rejected task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&rejected))
	assert.Equal(t, task.StatusInProgress, rejected.Status)
	assert.Equal(t, task.AwaitingAgent, rejected.Awaiting)

	// Rejecting a task that's awaiting=human takes it OUT of the queue
	// (it now awaits the agent).
	rr = doReq(f.router, http.MethodGet, "/api/v1/review-queue", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var q3 reviewQueueResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&q3))
	assert.Equal(t, 0, q3.Count, "after reject, task awaits the agent — not the queue")
}

// 3) /review-queue requires auth.
func TestReviewQueue_RequiresAuth(t *testing.T) {
	f := columnDeps(t)
	for _, path := range []string{"/api/v1/review-queue", "/api/v1/review-queue/count"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		f.router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code, path)
	}
}

// 4) Inbox tasks awaiting human are in the queue with empty project name.
func TestReviewQueue_IncludesInbox(t *testing.T) {
	f := columnDeps(t)

	// Create an inbox task and put it in awaiting=human by hand.
	rr := doReq(f.router, http.MethodPost, "/api/v1/inbox/tasks", f.cookie,
		map[string]any{"title": "queued from inbox"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var inbox task.Task
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&inbox))

	// PATCH it to awaiting=human + status=review to simulate an
	// inbox card that an agent has worked on.
	rr = doReq(f.router, http.MethodPatch, "/api/v1/tasks/"+inbox.ID, f.cookie,
		map[string]any{"status": "review", "awaiting": "human"})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	rr = doReq(f.router, http.MethodGet, "/api/v1/review-queue", f.cookie, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var got reviewQueueResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Equal(t, 1, got.Count)
	assert.Equal(t, inbox.ID, got.Tasks[0].Task.ID)
	assert.Equal(t, "", got.Tasks[0].ProjectName, "inbox row carries no project name")
	assert.Equal(t, "", got.Tasks[0].ProjectColor)
}
