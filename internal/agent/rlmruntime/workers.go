package rlmruntime

import (
	"context"
	"fmt"
	"strings"
)

func (m *Manager) RunWorkerFromCheckpoint(
	ctx context.Context,
	sessionID, branchID, checkpointID string,
	fn func(context.Context, *WorkerRuntime) (PublishPayload, error),
) (PublishPayload, error) {
	if fn == nil {
		return PublishPayload{}, fmt.Errorf("worker function is required")
	}

	select {
	case m.workerSem <- struct{}{}:
		defer func() { <-m.workerSem }()
	case <-ctx.Done():
		return PublishPayload{}, ctx.Err()
	}

	m.mu.Lock()
	factory := m.replFactory
	m.mu.Unlock()
	if factory == nil {
		return PublishPayload{}, fmt.Errorf("worker repl factory is not configured")
	}

	manifest, state, _, replay, err := m.store.LoadCheckpoint(ctx, sessionID, branchID, checkpointID)
	if err != nil {
		return PublishPayload{}, err
	}

	repl, err := factory()
	if err != nil {
		return PublishPayload{}, err
	}
	if err := repl.ImportState(state); err != nil {
		return PublishPayload{}, err
	}
	for _, block := range replay {
		if strings.TrimSpace(block) == "" {
			continue
		}
		if _, err := repl.Execute(ctx, block); err != nil {
			return PublishPayload{}, err
		}
	}

	worker := &WorkerRuntime{
		SessionID:  sessionID,
		BranchID:   branchID,
		Checkpoint: manifest.ID,
		REPL:       repl,
		State:      state,
	}

	payload, err := fn(ctx, worker)
	if err != nil {
		return PublishPayload{}, err
	}
	if payload.WorkerBranchID == "" {
		payload.WorkerBranchID = branchID
	}

	publishID, err := m.Publish(ctx, sessionID, payload)
	if err != nil {
		return PublishPayload{}, err
	}
	payload.ID = publishID
	return payload, nil
}

func (m *Manager) WorkerLimit() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.workerLimit
}

func (m *Manager) WorkerUsage() int {
	return len(m.workerSem)
}
