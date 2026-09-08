// Package api — Phase 15 handlers for task dependencies and the
// agent-facing tasks listing.
//
// Two endpoints:
//
//	PUT /api/v1/tasks/{id}/dependencies   user-side, replace blocker set
//	GET /api/v1/tasks/{id}/blockers       user-side, read current blockers
//	GET /api/v1/tasks/{id}/dependents     user-side, read reverse edges
//	GET /api/v1/agent/tasks                agent-side, list with ready/blocked filter
//
// The PUT endpoint runs the cycle check (service.SetTaskDependencies)
// before mutating; cycles + self-dependencies surface as 422.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"

	taskservice "github.com/ramgml/orenda/internal/service/task"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
)

// dependenciesRequest is the body of PUT /tasks/{id}/dependencies.
type dependenciesRequest struct {
	DependsOnIDs []string `json:"depends_on_ids"`
}

// putTaskDependenciesHandler replaces the task's full blocker set.
//
//	{ "depends_on_ids": ["task-A", "task-B"] }   — replace
//	{ "depends_on_ids": [] }                     — clear all
//
// Returns 200 with the new blockers list (Phase 15.4: the WS event
// already invalidates client caches; we still echo the new state so
// callers don't have to re-fetch).
func putTaskDependenciesHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TaskService == nil || deps.Tasks == nil {
			http.Error(w, "task deps not wired", http.StatusServiceUnavailable)
			return
		}
		taskID := chi.URLParam(r, "id")
		var req dependenciesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		// Allow missing body → empty list, but reject explicit null.
		if req.DependsOnIDs == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "depends_on_ids_required"})
			return
		}
		if err := deps.TaskService.SetTaskDependencies(r.Context(), taskID, req.DependsOnIDs); err != nil {
			// Cycle / self-dependency from the service: 422.
			if errors.Is(err, taskservice.ErrDependencyCycle) ||
				errors.Is(err, taskservice.ErrSelfDependency) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_dependency"})
				return
			}
			if errors.Is(err, taskservice.ErrNotFound) || errors.Is(err, task.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
				return
			}
			writeError(w, err)
			return
		}
		blockers, err := deps.Tasks.Blockers(r.Context(), taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"blockers": blockers})
	}
}

// getTaskBlockersHandler returns ALL blockers (open + satisfied).
//
// Phase 17 wires the TaskCard's "blocked" badge through this
// endpoint via BlockedByList; Phase 27.8 lets the agent claim-flow
// surface unfinished blockers in the 422 response. Future work
// (filtering by project, etc.) builds on this same passthrough.
func getTaskBlockersHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		taskID := chi.URLParam(r, "id")
		blockers, err := deps.Tasks.Blockers(r.Context(), taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"blockers": blockers})
	}
}

// getTaskDependentsHandler returns the list of task ids that depend
// on this task (reverse lookup). Useful for "finishing this unblocks
// N tasks" in the UI.
func getTaskDependentsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		taskID := chi.URLParam(r, "id")
		ids, err := deps.Tasks.Dependents(r.Context(), taskID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"dependents": ids})
	}
}

// listAgentTasksHandler returns the bearer agent's work surface.
//
//	GET /api/v1/agent/tasks?ready=true        only claimable tasks
//	GET /api/v1/agent/tasks?limit=100         cap (default 100, max 500)
//	GET /api/v1/agent/tasks?project_id=<uuid> scope to one project
//	GET /api/v1/agent/tasks?project=7|P7|<uuid>
//	GET /api/v1/agent/tasks?group_by=project  grouped by project
//	GET /api/v1/agent/tasks?group_by=project&tree=true  nested by parent
//
// "ready" means: status NOT IN (done, review, in_progress) AND
// no unfinished blockers AND no current lock holder. Useful for
// the agent inbox: "what can I claim right now?". Without ?ready
// the response is the full list — the agent then has to do the
// filtering itself.
//
// Task 140 (agent-project-scope): the surface only carries tasks the
// agent may actually act on. A project counts as accessible when it
// is open to all agents (agents_allowed = 1) or the agent holds an
// explicit grant row. Tasks of inaccessible projects are filtered
// out; inbox tasks (no project) stay visible. ?project_id accepts a
// UUID, ?project a number ("7"), a P-number ("P7"/"p7") or a UUID;
// passing both is a 400, an unresolvable reference a 404. A project
// filter doubles as the existence check — an inaccessible project is
// indistinguishable from an empty one.
//
// Task 153 (grouped listing): ?group_by=project reshapes the same
// selection into {groups: [{project, tasks}]}; the flat shape stays
// the default (bit-for-bit backwards compatible). Inbox tasks form
// their own trailing group with project:null and label "inbox".
// ?tree=true nests each group's tasks by parent_task_id (epics carry
// their children); a task whose parent is missing from the selection
// is a root flagged orphaned=true, a task caught in a parent cycle is
// a root flagged cyclic=true. tree requires group_by=project — a
// 400 invalid_input otherwise (T140 pattern: explicit errors, not
// silent reshaping).
func listAgentTasksHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}

		q := parseAgentTaskListParams(r.URL.Query())
		if q.badRequest {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
			return
		}
		scopedProject, ok := resolveAgentTaskScope(r.Context(), deps, w, q)
		if !ok {
			return
		}

		// Task 140: access set once per request — the visibility
		// filter and the project-scoped lookup share it.
		accessSet, err := deps.Projects.AgentAccessibleProjectIDs(r.Context(), id.AgentID)
		if err != nil {
			writeError(w, err)
			return
		}
		if scopedProject != nil && !accessSet[scopedProject.ID] {
			// An inaccessible project looks empty — do not leak its
			// existence or task count.
			writeJSON(w, http.StatusOK, map[string]any{"tasks": []any{}, "count": 0})
			return
		}

		tasks, err := listAgentTaskCandidates(r.Context(), deps, scopedProject)
		if err != nil {
			writeError(w, err)
			return
		}

		out, err := buildAgentTaskRows(r.Context(), deps, tasks, accessSet, id.AgentID, q.readyOnly)
		if err != nil {
			writeError(w, err)
			return
		}
		if len(out) > q.limit {
			out = out[:q.limit]
		}
		if q.groupBy == "project" {
			writeJSON(w, http.StatusOK, buildGroupedResponse(deps, r, out, q.tree))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tasks": out,
			"count": len(out),
		})
	}
}

// allDigits reports whether s is a non-empty ASCII digit sequence —
// the bare-number spelling of a project reference ("project=7").
// ParseProjectRef covers only the "P7" form.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// agentTaskListParams is the parsed query of GET /agent/tasks.
// limit defaults to 100 (values outside (0,500] keep the default);
// readyOnly mirrors ?ready=true. group_by accepts only "project"
// and tree only "true"/"false" — anything else sets badRequest (an
// explicit 400, the T140 rule applied to values). tree requires
// group_by=project. Passing both project_id and project is
// ambiguous input and also a badRequest.
type agentTaskListParams struct {
	limit      int
	readyOnly  bool
	groupBy    string
	tree       bool
	projectID  string
	projectRef string
	badRequest bool
}

// parseAgentTaskListParams parses the query parameters of the agent
// task listing. It never writes a response; the caller turns
// badRequest into the 400 invalid_input reply.
func parseAgentTaskListParams(values url.Values) agentTaskListParams {
	q := agentTaskListParams{limit: 100}
	if v := values.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			q.limit = n
		}
	}
	q.readyOnly = values.Get("ready") == "true"

	q.groupBy = values.Get("group_by")
	if q.groupBy != "" && q.groupBy != "project" {
		q.badRequest = true
		return q
	}
	treeParam := values.Get("tree")
	switch treeParam {
	case "", "false", "true":
	default:
		q.badRequest = true
		return q
	}
	q.tree = treeParam == "true"
	if q.tree && q.groupBy == "" {
		q.badRequest = true
		return q
	}

	q.projectID = values.Get("project_id")
	q.projectRef = values.Get("project")
	if q.projectID != "" && q.projectRef != "" {
		q.badRequest = true
		return q
	}
	return q
}

// resolveAgentTaskScope resolves the optional project scope of the
// agent task listing (Task 140). The reference forms mirror the
// path resolution elsewhere (resolveProjectRef + GetByNumber);
// resolution failures are reported through writeProjectResolveError
// exactly like the user-side handlers, and an unparseable bare
// number is a 404 not_found. ok=false means a response was written.
func resolveAgentTaskScope(ctx context.Context, deps *Dependencies, w http.ResponseWriter, q agentTaskListParams) (*project.Project, bool) {
	if q.projectID != "" {
		p, err := deps.Projects.GetProject(ctx, q.projectID)
		if err != nil {
			writeProjectResolveError(w, err)
			return nil, false
		}
		return p, true
	}
	if q.projectRef == "" {
		return nil, true
	}
	var (
		p   *project.Project
		err error
	)
	if n, ok := project.ParseProjectRef(q.projectRef); ok {
		p, err = deps.Projects.GetByNumber(ctx, n)
	} else if allDigits(q.projectRef) {
		// Bare number form ("project=7") — ParseProjectRef
		// only covers the P-prefixed spelling.
		n, aerr := strconv.Atoi(q.projectRef)
		if aerr != nil || n <= 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return nil, false
		}
		p, err = deps.Projects.GetByNumber(ctx, n)
	} else {
		p, err = deps.Projects.GetProject(ctx, q.projectRef)
	}
	if err != nil {
		writeProjectResolveError(w, err)
		return nil, false
	}
	return p, true
}

// listAgentTaskCandidates lists the claimable tasks of the agent
// surface. With a project scope one query returns exactly that
// project's claimable tasks (inbox is NOT appended — a scoped
// listing never mixes in no-project tasks). Without a scope the
// full surface is listed and the inbox tasks are merged in
// order-stable by id — first occurrence wins (T151: merging query 2
// raw used to duplicate the inbox tasks already carried by query 1
// and double the count). Phase 33.1: AssigneeTypeIncludeNull — an
// unassigned todo task is claimable by any agent.
func listAgentTaskCandidates(ctx context.Context, deps *Dependencies, scopedProject *project.Project) ([]*task.Task, error) {
	if scopedProject != nil {
		f := task.Filter{ProjectID: scopedProject.ID, Status: task.StatusTodo, AssigneeType: task.AssigneeAgent, AssigneeTypeIncludeNull: true}
		return deps.Tasks.ListByProject(ctx, f)
	}
	f := task.Filter{Status: task.StatusTodo, AssigneeType: task.AssigneeAgent, AssigneeTypeIncludeNull: true}
	tasks, err := deps.Tasks.ListByProject(ctx, f)
	if err != nil {
		return nil, err
	}
	// Also include inbox tasks: Filter has NoProject for that.
	// Task 151: query 1 has no project clause, so inbox tasks
	// with assignee_type agent-or-NULL are already in `tasks`.
	f2 := task.Filter{NoProject: true, Status: task.StatusTodo}
	inboxTasks, err := deps.Tasks.ListByProject(ctx, f2)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(tasks)+len(inboxTasks))
	for _, tr := range tasks {
		seen[tr.ID] = struct{}{}
	}
	for _, tr := range inboxTasks {
		if _, dup := seen[tr.ID]; dup {
			continue
		}
		seen[tr.ID] = struct{}{}
		tasks = append(tasks, tr)
	}
	return tasks, nil
}

// buildAgentTaskRows hydrates the candidate tasks with their
// blockers (one batched round-trip, Phase 28.22) and computes the
// per-row ready flag: no unfinished blockers, not status=blocked
// (Task 115: both signals are checked so the ready list never
// lies), and not assigned — to another agent (Phase 15) nor to the
// calling agent itself (in-flight work is noise in the ready list).
// Tasks of inaccessible projects are invisible (Task 140); inbox
// tasks are always shown. With readyOnly set, rows that are not
// ready are skipped.
func buildAgentTaskRows(ctx context.Context, deps *Dependencies, tasks []*task.Task, accessSet map[string]bool, agentID string, readyOnly bool) ([]taskRow, error) {
	ids := make([]string, 0, len(tasks))
	for _, tr := range tasks {
		ids = append(ids, tr.ID)
	}
	blockersByTask, err := deps.Tasks.BlockersForTasks(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]taskRow, 0, len(tasks))
	for _, tr := range tasks {
		if tr.ProjectID != "" && !accessSet[tr.ProjectID] {
			continue
		}
		var blockedBy []string
		ready := true
		for _, b := range blockersByTask[tr.ID] {
			if !b.Done {
				blockedBy = append(blockedBy, b.BlockerID)
				ready = false
			}
		}
		if ready && tr.Status == task.StatusBlocked {
			ready = false
		}
		if ready && tr.AssigneeType == task.AssigneeAgent && tr.AssigneeID != "" && tr.AssigneeID != agentID {
			ready = false
		}
		if ready && tr.AssigneeType == task.AssigneeAgent && tr.AssigneeID == agentID {
			ready = false
		}
		if readyOnly && !ready {
			continue
		}
		out = append(out, taskRow{Task: tr, BlockedBy: blockedBy, Ready: ready})
	}
	return out, nil
}

// --- T153: grouped / tree response shaping -----------------------------

// taskRow mirrors the anonymous row of the flat listing: the grouped
// shape re-uses the exact same per-task payload so consumers see one
// task JSON regardless of the listing form.
type taskRow = struct {
	Task      *task.Task `json:"task"`
	BlockedBy []string   `json:"blocked_by"`
	Ready     bool       `json:"ready"`
}

// taskGroup is one project slice of the grouped listing. The inbox
// slice carries Project == nil and Label == "inbox".
type taskGroup struct {
	Project *project.Project `json:"project"`
	Label   string           `json:"label,omitempty"`
	Tasks   []taskRow        `json:"tasks"`
	Tree    []taskNode       `json:"tree,omitempty"`
}

// taskNode is one node of a group's tree. The task row fields are
// inlined at the same keys as the flat listing ("task", "blocked_by",
// "ready") plus children/flags, so consumers parse one node shape.
// Orphaned marks a root whose parent exists but fell outside the
// selection; cyclic marks a root that is part of a parent cycle.
type taskNode struct {
	TaskRow  taskRow    `json:"-"`
	Children []taskNode `json:"children,omitempty"`
	Orphaned bool       `json:"orphaned,omitempty"`
	Cyclic   bool       `json:"cyclic,omitempty"`
}

// MarshalJSON emits the node as its task row ("task", "blocked_by",
// "ready") plus the tree fields ("children", "orphaned", "cyclic").
// Built as a plain map to avoid brittle string splicing.
func (n taskNode) MarshalJSON() ([]byte, error) {
	row, err := json.Marshal(n.TaskRow)
	if err != nil {
		return nil, err
	}
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(row, &m); err != nil {
		return nil, err
	}
	if n.Children != nil {
		c, err := json.Marshal(n.Children)
		if err != nil {
			return nil, err
		}
		m["children"] = c
	}
	if n.Orphaned {
		m["orphaned"], _ = json.Marshal(true)
	}
	if n.Cyclic {
		m["cyclic"], _ = json.Marshal(true)
	}
	return json.Marshal(m)
}

// buildGroupedResponse groups the flat rows by project (ordered by
// project number, inbox last) and — when tree is set — nests each
// group's tasks by parent_task_id. Project metadata comes from the
// same repo the flat path uses; a project that vanished between the
// task query and the lookup degrades to an id-only stub rather than
// dropping the group.
func buildGroupedResponse(deps *Dependencies, r *http.Request, rows []taskRow, tree bool) map[string]any {
	byProject := make(map[string][]taskRow)
	order := make([]string, 0, 4) // distinct project ids, first-seen order
	var inbox []taskRow
	for _, row := range rows {
		if row.Task.ProjectID == "" {
			inbox = append(inbox, row)
			continue
		}
		pid := row.Task.ProjectID
		if _, seen := byProject[pid]; !seen {
			order = append(order, pid)
		}
		byProject[pid] = append(byProject[pid], row)
	}

	// Deterministic group order: project number ascending.
	metas := make(map[string]*project.Project, len(order))
	nums := make(map[string]int, len(order))
	for _, pid := range order {
		p, err := deps.Projects.GetProject(r.Context(), pid)
		if err != nil {
			// Project deleted concurrently — keep the tasks visible
			// under an id-stub instead of silently hiding them.
			metas[pid] = &project.Project{ID: pid}
			nums[pid] = int(^uint(0) >> 1) // maxint → sorts last
			continue
		}
		metas[pid] = p
		nums[pid] = p.Number
	}
	sort.SliceStable(order, func(i, j int) bool { return nums[order[i]] < nums[order[j]] })

	groups := make([]taskGroup, 0, len(order)+1)
	for _, pid := range order {
		g := taskGroup{Project: metas[pid], Tasks: byProject[pid]}
		if tree {
			g.Tree = buildTaskTree(g.Tasks)
		}
		groups = append(groups, g)
	}
	if len(inbox) > 0 {
		g := taskGroup{Tasks: inbox, Label: "inbox"}
		if tree {
			g.Tree = buildTaskTree(inbox)
		}
		groups = append(groups, g)
	}
	return map[string]any{"groups": groups, "count": len(rows)}
}

// buildTaskTree nests group rows by parent_task_id. Only parents
// within the same group's selection become inner nodes; a row whose
// parent is missing from the selection is a root flagged
// orphaned=true. Cycles cannot hang the walk: members of a parent
// cycle are hoisted to roots flagged cyclic=true. Roots keep
// first-seen order.
func buildTaskTree(rows []taskRow) []taskNode {
	byID := make(map[string]*taskRow, len(rows))
	for i := range rows {
		byID[rows[i].Task.ID] = &rows[i]
	}
	children := make(map[string][]*taskRow)
	inCycle := make(map[string]bool)
	for i := range rows {
		tr := &rows[i]
		parentID := tr.Task.ParentTaskID
		if parentID == "" || parentID == tr.Task.ID || byID[parentID] == nil {
			continue
		}
		children[parentID] = append(children[parentID], tr)
		// Cycle check along the parent chain from this node.
		seen := map[string]bool{tr.Task.ID: true}
		cur := parentID
		for cur != "" && byID[cur] != nil {
			if seen[cur] {
				// Re-entered a visited node — walk the loop once and
				// mark every member cyclic.
				m := map[string]bool{cur: true}
				n := byID[cur].Task.ParentTaskID
				for n != "" && byID[n] != nil && !m[n] {
					m[n] = true
					n = byID[n].Task.ParentTaskID
				}
				for k := range m {
					inCycle[k] = true
				}
				break
			}
			seen[cur] = true
			cur = byID[cur].Task.ParentTaskID
		}
	}
	var roots []*taskRow
	for i := range rows {
		tr := &rows[i]
		parentID := tr.Task.ParentTaskID
		switch {
		case parentID == "" || parentID == tr.Task.ID:
			roots = append(roots, tr)
		case byID[parentID] == nil:
			// Parent outside the selection — orphaned root.
			roots = append(roots, tr)
		case inCycle[tr.Task.ID]:
			// Cycle member — hoisted to a root, flagged below.
			roots = append(roots, tr)
		}
	}
	var build func(row *taskRow, depth int) taskNode
	build = func(row *taskRow, depth int) taskNode {
		node := taskNode{TaskRow: *row}
		if depth > 64 { // paranoia bound; chains are short in practice
			return node
		}
		for _, c := range children[row.Task.ID] {
			if c.Task.ID == row.Task.ID {
				continue // self-parent guard (already a root)
			}
			node.Children = append(node.Children, build(c, depth+1))
		}
		return node
	}
	out := make([]taskNode, 0, len(roots))
	for _, root := range roots {
		n := build(root, 0)
		if inCycle[root.Task.ID] {
			n.Cyclic = true
		}
		if root.Task.ParentTaskID != "" && !inCycle[root.Task.ID] && byID[root.Task.ParentTaskID] == nil {
			n.Orphaned = true
		}
		out = append(out, n)
	}
	return out
}
