package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/zfdang/agent-registry/pkg/client/pg"
	"github.com/zfdang/agent-registry/pkg/model/db"
)

// TaskInput is the parsed { skill, ...params } data part of an A2A
// tasks/send message (07).
type TaskInput struct {
	Skill string          `json:"skill"`
	Raw   json.RawMessage `json:"-"`
}

// KnownSkills are the registry's own A2A skills (07).
var KnownSkills = map[string]bool{
	db.SkillSearchAgents:  true,
	db.SkillGetAgent:      true,
	db.SkillRegisterAgent: true,
}

// CreateTask implements tasks/send (07): it creates a task (idempotent on
// clientID), and for the two fast skills runs them to completion
// synchronously; register-agent is enqueued for the async worker.
func (s *Service) CreateTask(ctx context.Context, clientID *string, skill string, input json.RawMessage, clientIP string) (*db.A2ATask, error) {
	if !KnownSkills[skill] {
		return nil, ErrUnknownSkill
	}

	if clientID != nil {
		if existing, err := s.store.GetTask(ctx, *clientID); err == nil {
			return existing, nil // idempotency key: resending returns the existing task
		} else if !pg.IsNotFound(err) {
			return nil, err
		}
	}

	id := db.NewID(db.TaskIDPrefix)
	if clientID != nil {
		id = *clientID
	}

	now := time.Now().UTC()
	task := &db.A2ATask{
		ID:        id,
		Skill:     skill,
		Status:    db.TaskSubmitted,
		Input:     input,
		ClientIP:  clientIP,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}

	switch skill {
	case db.SkillSearchAgents, db.SkillGetAgent:
		s.runSyncSkill(ctx, task)
	case db.SkillRegisterAgent:
		if s.worker != nil {
			s.worker.Enqueue(task.ID)
		}
	}

	fresh, err := s.store.GetTask(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	return fresh, nil
}

// GetTask implements tasks/get (07).
func (s *Service) GetTask(ctx context.Context, id string) (*db.A2ATask, error) {
	t, err := s.store.GetTask(ctx, id)
	if err != nil {
		if pg.IsNotFound(err) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}
	return t, nil
}

// CancelTask implements tasks/cancel (07): only submitted/working tasks
// are cancelable. If a worker is actively running this task in this
// process, its context is canceled so the pipeline stops between stages.
func (s *Service) CancelTask(ctx context.Context, id string) (*db.A2ATask, error) {
	t, err := s.store.GetTask(ctx, id)
	if err != nil {
		if pg.IsNotFound(err) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}

	switch t.Status {
	case db.TaskSubmitted, db.TaskWorking:
		ok, err := s.store.CASTaskStatus(ctx, id, t.Status, db.TaskCanceled)
		if err != nil {
			return nil, err
		}
		if !ok {
			// Lost a race with the worker landing a terminal state.
			return nil, ErrTaskNotCancelable
		}
		if s.worker != nil {
			s.worker.cancelIfRunning(id)
		}
	default:
		return nil, ErrTaskNotCancelable
	}

	return s.store.GetTask(ctx, id)
}

// runSyncSkill executes search-agents/get-agent synchronously and lands the
// task completed/failed before CreateTask returns (07: "typically complete
// before the tasks/send response is written").
func (s *Service) runSyncSkill(ctx context.Context, task *db.A2ATask) {
	var artifact json.RawMessage
	status := db.TaskCompleted

	switch task.Skill {
	case db.SkillSearchAgents:
		var in struct {
			Query  string `json:"query"`
			Status string `json:"status"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		_ = json.Unmarshal(task.Input, &in)
		if in.Limit <= 0 || in.Limit > 50 {
			in.Limit = 10
		}
		page := in.Offset/max(in.Limit, 1) + 1
		resp, err := s.ListAgents(ctx, in.Query, in.Status, "newest", page, in.Limit)
		if err != nil {
			status = db.TaskFailed
			artifact = errorArtifact("internal_error", err.Error())
		} else {
			artifact, _ = json.Marshal(map[string]any{"agents": resp.Agents, "total": resp.Total})
		}

	case db.SkillGetAgent:
		var in struct {
			Ref string `json:"ref"`
		}
		_ = json.Unmarshal(task.Input, &in)
		if in.Ref == "" {
			status = db.TaskFailed
			artifact = errorArtifact("invalid_input", "ref is required")
			break
		}
		agent, err := s.GetAgent(ctx, in.Ref)
		if err != nil {
			status = db.TaskFailed
			artifact = errorForResolveErr(err)
		} else {
			checks, _ := s.GetVerification(ctx, in.Ref)
			artifact, _ = json.Marshal(map[string]any{"agent": agent, "verification": checks})
		}
	}

	task.Status = status
	task.Result = artifact
	task.UpdatedAt = time.Now().UTC()
	_ = s.store.UpdateTask(ctx, task)
}

func errorArtifact(code, message string) json.RawMessage {
	d, _ := json.Marshal(map[string]any{"code": code, "message": message})
	return d
}

func errorForResolveErr(err error) json.RawMessage {
	var amb *AmbiguousRefError
	if asAmbiguous(err, &amb) {
		d, _ := json.Marshal(map[string]any{"code": "ambiguous_ref", "candidates": amb.Candidates})
		return d
	}
	return errorArtifact("not_found", "no agent matches ref")
}

func asAmbiguous(err error, target **AmbiguousRefError) bool {
	if e, ok := err.(*AmbiguousRefError); ok {
		*target = e
		return true
	}
	return false
}

// TaskWorker runs register-agent tasks asynchronously: a channel carries
// freshly submitted task ids, and a poll loop picks up any submitted task
// left behind by a process restart (07).
type TaskWorker struct {
	svc *Service
	ch  chan string

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// NewTaskWorker builds a worker bound to svc.
func NewTaskWorker(svc *Service) *TaskWorker {
	return &TaskWorker{
		svc:     svc,
		ch:      make(chan string, 256),
		running: make(map[string]context.CancelFunc),
	}
}

// Enqueue signals the worker to process taskID soon. It never blocks —
// if the channel is full, the poll loop will still pick the task up.
func (w *TaskWorker) Enqueue(taskID string) {
	select {
	case w.ch <- taskID:
	default:
	}
}

// Start runs the channel consumer and the restart-recovery poll loop until
// ctx is canceled.
func (w *TaskWorker) Start(ctx context.Context) {
	go w.consumeLoop(ctx)
	go w.pollLoop(ctx)
}

func (w *TaskWorker) consumeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-w.ch:
			w.process(ctx, id)
		}
	}
}

func (w *TaskWorker) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tasks, err := w.svc.store.ListTasksByStatus(ctx, db.TaskSubmitted, 20)
			if err != nil {
				w.svc.log.Errorw("worker: poll failed", "error", err)
				continue
			}
			for _, t := range tasks {
				w.process(ctx, t.ID)
			}
		}
	}
}

func (w *TaskWorker) cancelIfRunning(taskID string) {
	w.mu.Lock()
	cancel, ok := w.running[taskID]
	w.mu.Unlock()
	if ok {
		cancel()
	}
}

// process claims a submitted task (CAS to working) and runs the register
// pipeline against it, landing completed/failed/canceled.
func (w *TaskWorker) process(parent context.Context, taskID string) {
	ok, err := w.svc.store.CASTaskStatus(parent, taskID, db.TaskSubmitted, db.TaskWorking)
	if err != nil {
		w.svc.log.Errorw("worker: claim failed", "task", taskID, "error", err)
		return
	}
	if !ok {
		return // already claimed, or no longer submitted
	}

	taskCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	w.mu.Lock()
	w.running[taskID] = cancel
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.running, taskID)
		w.mu.Unlock()
		cancel()
	}()

	task, err := w.svc.store.GetTask(taskCtx, taskID)
	if err != nil {
		w.svc.log.Errorw("worker: reload task failed", "task", taskID, "error", err)
		return
	}

	var in struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(task.Input, &in); err != nil || in.URL == "" {
		w.land(task, db.TaskFailed, errorArtifact("invalid_input", "url is required"))
		return
	}

	result, err := w.svc.RegisterAgent(taskCtx, in.URL)
	if err != nil {
		if taskCtx.Err() != nil {
			// Canceled between pipeline stages (05, 07).
			_, _ = w.svc.store.CASTaskStatus(context.Background(), taskID, db.TaskWorking, db.TaskCanceled)
			return
		}
		w.land(task, db.TaskFailed, errorArtifact("internal_error", err.Error()))
		return
	}

	artifact, _ := json.Marshal(result)
	if !result.Registered {
		w.land(task, db.TaskFailed, artifact)
		return
	}
	w.land(task, db.TaskCompleted, artifact)
}

func (w *TaskWorker) land(task *db.A2ATask, status string, artifact json.RawMessage) {
	task.Status = status
	task.Result = artifact
	task.UpdatedAt = time.Now().UTC()
	if err := w.svc.store.UpdateTask(context.Background(), task); err != nil {
		w.svc.log.Errorw("worker: land task failed", "task", task.ID, "error", err)
	}
}
