package rlmruntime

import "context"

type RuntimeInspection struct {
	Runtime   RuntimeState     `json:"runtime"`
	Branches  []BranchState    `json:"branches"`
	Published []PublishPayload `json:"published"`
}

func (m *Manager) InspectRuntime(ctx context.Context, sessionID string) (RuntimeInspection, error) {
	runtime, err := m.LoadOrCreate(ctx, sessionID)
	if err != nil {
		return RuntimeInspection{}, err
	}
	branches, err := m.store.ListBranches(ctx, sessionID)
	if err != nil {
		return RuntimeInspection{}, err
	}
	return RuntimeInspection{
		Runtime:   runtime.State,
		Branches:  branches,
		Published: m.ListPublished(sessionID),
	}, nil
}

func (m *Manager) InspectBranchJournal(ctx context.Context, sessionID, branchID string, limit int) ([]JournalEntry, error) {
	return m.store.ReadJournal(ctx, sessionID, branchID, limit)
}
