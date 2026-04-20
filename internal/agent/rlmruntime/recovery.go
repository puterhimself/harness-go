package rlmruntime

import "context"

// RecoverSession reloads runtime metadata and rehydrates active-branch state.
func (m *Manager) RecoverSession(ctx context.Context, sessionID string) (*SessionRuntime, error) {
	runtime, err := m.LoadOrCreate(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	if m.sessionEvents != nil {
		sessionObj, err := m.sessionEvents.GetSession(ctx, sessionID)
		if err == nil && sessionObj != nil && sessionObj.ActiveBranchID != "" && sessionObj.ActiveBranchID != runtime.State.ActiveBranchID {
			runtime.State.ActiveBranchID = sessionObj.ActiveBranchID
			if err := m.store.SaveRuntime(ctx, runtime.State); err != nil {
				return nil, err
			}
			if runtime.REPL != nil {
				if _, err := m.ensureRuntimeBridge(ctx, runtime, ""); err != nil {
					return nil, err
				}
				if err := m.restoreActiveBranch(ctx, runtime); err != nil {
					return nil, err
				}
			}
		}
	}

	return runtime, nil
}

// RecoverActiveBranch returns the active branch id after recovery.
func (m *Manager) RecoverActiveBranch(ctx context.Context, sessionID string) (string, error) {
	runtime, err := m.RecoverSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return runtime.State.ActiveBranchID, nil
}
