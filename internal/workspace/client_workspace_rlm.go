package workspace

import "context"

func (w *ClientWorkspace) InspectRuntime(ctx context.Context, sessionID string, journalLimit int) (RLMRuntimeInspection, error) {
	_ = ctx
	_ = sessionID
	_ = journalLimit
	return RLMRuntimeInspection{}, ErrRLMRuntimeUnavailable
}

func (w *ClientWorkspace) ForkRuntimeBranch(ctx context.Context, sessionID, name string) (RLMRuntimeBranch, error) {
	_ = ctx
	_ = sessionID
	_ = name
	return RLMRuntimeBranch{}, ErrRLMRuntimeUnavailable
}

func (w *ClientWorkspace) SwitchRuntimeBranch(ctx context.Context, sessionID, branchID string) error {
	_ = ctx
	_ = sessionID
	_ = branchID
	return ErrRLMRuntimeUnavailable
}

func (w *ClientWorkspace) ResumeRuntimeBranch(ctx context.Context, sessionID, branchID string) error {
	_ = ctx
	_ = sessionID
	_ = branchID
	return ErrRLMRuntimeUnavailable
}

var _ RLMRuntime = (*ClientWorkspace)(nil)
