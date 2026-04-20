package rlmruntime

import "context"

type RuntimeInspection struct {
	Runtime            RuntimeState     `json:"runtime"`
	Branches           []BranchState    `json:"branches"`
	Published          []PublishPayload `json:"published"`
	ActiveTrace        EpisodeTrace     `json:"active_trace"`
	ActiveCheckpointID string           `json:"active_checkpoint_id,omitempty"`
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
	activeCheckpointID := ""
	activeTrace := EpisodeTrace{}
	if branch, ok := runtime.Branches[runtime.State.ActiveBranchID]; ok {
		activeCheckpointID = branch.HeadCheckpoint
		if branch.HeadCheckpoint != "" {
			trace, traceErr := m.store.LoadCheckpointTrace(ctx, sessionID, branch.BranchID, branch.HeadCheckpoint)
			if traceErr != nil {
				return RuntimeInspection{}, traceErr
			}
			activeTrace = trace
		}
	}
	return RuntimeInspection{
		Runtime:            runtime.State,
		Branches:           branches,
		Published:          m.ListPublished(sessionID),
		ActiveTrace:        activeTrace,
		ActiveCheckpointID: activeCheckpointID,
	}, nil
}

func (m *Manager) InspectBranchJournal(ctx context.Context, sessionID, branchID string, limit int) ([]JournalEntry, error) {
	return m.store.ReadJournal(ctx, sessionID, branchID, limit)
}
