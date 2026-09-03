package api_test

// T153: ?group_by=project (and ?tree=true) reshape the SAME selection
// the flat listing returns — nothing gained, nothing lost:
//   - two projects with tasks → two groups (+ inbox group last);
//   - inbox tasks form their own group (project null, label "inbox");
//   - per-group composition matches the flat list bit-for-bit;
//   - tree nests children under parents; parents outside the
//     selection surface as orphaned roots; parent cycles surface as
//     cyclic roots (no hang, nothing dropped);
//   - unknown group_by values / tree without group_by → 400;
//   - no params → flat shape unchanged (T151 tests still green).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
)

// fetchGrouped calls the agent listing and decodes the grouped shape.
func fetchGrouped(t *testing.T, fx *refFixture, query string) (int, []groupedGroup) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks"+query, nil)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		return rr.Code, nil
	}
	var resp struct {
		Groups []groupedGroup `json:"groups"`
		Count  int            `json:"count"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	return rr.Code, resp.Groups
}

type groupedGroup struct {
	Project *struct {
		ID     string `json:"id"`
		Number int    `json:"number"`
		Name   string `json:"name"`
	} `json:"project"`
	Label string `json:"label"`
	Tasks []struct {
		Task struct {
			ID     string `json:"id"`
			Number int    `json:"number"`
			Title  string `json:"title"`
		} `json:"task"`
	} `json:"tasks"`
	Tree []groupedNode `json:"tree"`
}

type groupedNode struct {
	Task struct {
		ID string `json:"id"`
	} `json:"task"`
	Children []groupedNode `json:"children"`
	Orphaned bool          `json:"orphaned"`
	Cyclic   bool          `json:"cyclic"`
}

// seedProjectTodo creates a todo task in the given project through the
// repo (direct; the fixture's own task creation path covers the API).
func seedProjectTodo(t *testing.T, fx *refFixture, projectID, title string) *task.Task {
	t.Helper()
	tr := &task.Task{Title: title, ProjectID: projectID, Status: task.StatusTodo, ColumnID: fx.todoColID}
	require.NoError(t, fx.tasks.Create(context.Background(), tr))
	return tr
}

// patchTask applies a partial update via repo Update (read-modify-write).
func patchTask(t *testing.T, fx *refFixture, id string, mutate func(*task.Task)) {
	t.Helper()
	tr, err := fx.tasks.GetByID(context.Background(), id)
	require.NoError(t, err)
	mutate(tr)
	require.NoError(t, fx.tasks.Update(context.Background(), tr))
}

// TestGroupBy_TwoProjectsAndInbox: two projects + inbox → three
// groups in order (projects by number, inbox last); group composition
// equals the flat list.
func TestGroupBy_TwoProjectsAndInbox(t *testing.T) {
	t.Parallel()
	fx := newListFixture(t)

	p2 := seedProjectTodo(t, fx, fx.projectID, "proj task A")
	// A second project (agent-open) with one task.
	p2proj, _, _, err := fx.projects.CreateProject(context.Background(), &project.Project{
		Name: "Grouped Two", OwnerID: fx.ownerID, AgentsAllowed: true,
	})
	require.NoError(t, err)
	p := struct{ ID string }{ID: p2proj.ID}
	b2 := seedProjectTodo(t, fx, p.ID, "proj task B")
	require.NoError(t, seedInboxTodo(t, fx, "inbox task"))

	code, groups := fetchGrouped(t, fx, "?group_by=project")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, groups, 3)

	// Projects by number: the fixture's project was created first.
	require.NotNil(t, groups[0].Project)
	require.NotNil(t, groups[1].Project)
	assert.Equal(t, fx.projectID, groups[0].Project.ID)
	assert.Equal(t, p.ID, groups[1].Project.ID)
	require.Nil(t, groups[2].Project)
	assert.Equal(t, "inbox", groups[2].Label)

	ids := func(g groupedGroup) []string {
		out := make([]string, 0, len(g.Tasks))
		for _, row := range g.Tasks {
			out = append(out, row.Task.ID)
		}
		return out
	}
	// Group 0 = fixture project: its seeded "Resolve me" + proj task A.
	assert.Equal(t, []string{fx.taskID, p2.ID}, ids(groups[0]))
	assert.Equal(t, []string{b2.ID}, ids(groups[1]))
	require.Len(t, groups[2].Tasks, 1)

	// Composition must equal the flat list.
	flat, count := fetchAgentListFor(t, fx, "")
	total := 0
	for _, g := range groups {
		total += len(g.Tasks)
	}
	assert.Equal(t, count, total)
	assert.Len(t, flat, total)
}

// TestGroupBy_InvalidInputs: unknown group_by value, unknown tree
// value and tree without group_by are all explicit 400s.
func TestGroupBy_InvalidInputs(t *testing.T) {
	t.Parallel()
	fx := newListFixture(t)

	for _, q := range []string{"?group_by=status", "?tree=yes", "?tree=true", "?group_by=project&tree=maybe"} {
		code, _ := fetchGrouped(t, fx, q)
		assert.Equal(t, http.StatusBadRequest, code, "query %s must be 400", q)
	}
	// tree=false alone is fine (no grouping → flat shape).
	code, _ := fetchGrouped(t, fx, "?tree=false")
	assert.Equal(t, http.StatusOK, code)
}

// TestGroupBy_Tree_NestsChildren: parent + child in one project →
// child nested under the parent in tree; the flat tasks array stays
// complete.
func TestGroupBy_Tree_NestsChildren(t *testing.T) {
	t.Parallel()
	fx := newListFixture(t)

	parent := seedProjectTodo(t, fx, fx.projectID, "epic")
	child := seedProjectTodo(t, fx, fx.projectID, "sub")
	patchTask(t, fx, child.ID, func(tr *task.Task) { tr.ParentTaskID = parent.ID })

	code, groups := fetchGrouped(t, fx, "?group_by=project&tree=true")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, groups, 1)
	// resolve-me (fixture) + the epic are roots; the sub nests under
	// the epic.
	require.Len(t, groups[0].Tree, 2, "epic + resolve-me are roots; sub nested")
	var epic *groupedNode
	for i := range groups[0].Tree {
		if groups[0].Tree[i].Task.ID == parent.ID {
			epic = &groups[0].Tree[i]
		}
	}
	require.NotNil(t, epic, "epic must be among the roots")
	require.Len(t, epic.Children, 1)
	assert.Equal(t, child.ID, epic.Children[0].Task.ID)
	// The flat list stays bit-for-bit complete (3 rows: fixture task
	// + epic + sub).
	assert.Len(t, groups[0].Tasks, 3)
}

// TestGroupBy_Tree_OrphanAndCycle: a child whose parent is outside
// the selection is an orphaned root; a parent↔child cycle surfaces
// both as cyclic roots and must not hang.
func TestGroupBy_Tree_OrphanAndCycle(t *testing.T) {
	t.Parallel()
	fx := newListFixture(t)

	// Orphan: parent task exists but is done → not on the todo
	// selection, child alone on the surface.
	doneParent := seedProjectTodo(t, fx, fx.projectID, "finished epic")
	orphan := seedProjectTodo(t, fx, fx.projectID, "orphan sub")
	patchTask(t, fx, orphan.ID, func(tr *task.Task) { tr.ParentTaskID = doneParent.ID })
	patchTask(t, fx, doneParent.ID, func(tr *task.Task) { tr.Status = task.StatusDone })

	// Cycle: a↔b.
	a := seedProjectTodo(t, fx, fx.projectID, "cycle a")
	b := seedProjectTodo(t, fx, fx.projectID, "cycle b")
	patchTask(t, fx, a.ID, func(tr *task.Task) { tr.ParentTaskID = b.ID })
	patchTask(t, fx, b.ID, func(tr *task.Task) { tr.ParentTaskID = a.ID })

	code, groups := fetchGrouped(t, fx, "?group_by=project&tree=true")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, groups, 1)

	byID := map[string]groupedNode{}
	for _, n := range groups[0].Tree {
		byID[n.Task.ID] = n
	}
	orphanNode, ok := byID[orphan.ID]
	require.True(t, ok, "orphan must be a root, tree=%v", groups[0].Tree)
	assert.True(t, orphanNode.Orphaned)
	aNode, ok := byID[a.ID]
	require.True(t, ok, "cycle member a must be a root")
	assert.True(t, aNode.Cyclic, "cycle member must be flagged cyclic")
	bNode, ok := byID[b.ID]
	require.True(t, ok, "cycle member b must be a root")
	assert.True(t, bNode.Cyclic)
	assert.False(t, orphanNode.Cyclic)
	// The finished epic must NOT appear (status=done filtered).
	assert.NotContains(t, byID, doneParent.ID)
}

// TestGroupBy_FlattDefaultUnchanged: without group_by the response
// keeps the flat shape (tasks + count, no groups key).
func TestGroupBy_FlatDefaultUnchanged(t *testing.T) {
	t.Parallel()
	fx := newListFixture(t)
	require.NoError(t, seedInboxTodo(t, fx, "inbox x"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Contains(t, body, "tasks")
	require.Contains(t, body, "count")
	assert.NotContains(t, body, "groups", "flat shape must not grow a groups key")

	var resp struct {
		Groups json.RawMessage `json:"groups"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	assert.Nil(t, resp.Groups)
}
