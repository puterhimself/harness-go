package rlmruntime

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	"github.com/google/uuid"
)

func (m *Manager) ForkBranch(ctx context.Context, sessionID, name string) (BranchState, error) {
	runtime, err := m.LoadOrCreate(ctx, sessionID)
	if err != nil {
		return BranchState{}, err
	}

	activeID := runtime.State.ActiveBranchID
	activeBranch, ok := runtime.Branches[activeID]
	if !ok {
		return BranchState{}, ErrBranchNotFound
	}

	branchID := uuid.NewString()
	if m.sessionEvents != nil {
		fromEntryID, forkErr := m.ensureForkEntry(ctx, sessionID, activeID)
		if forkErr != nil {
			return BranchState{}, forkErr
		}
		forked, forkErr := m.sessionEvents.ForkBranch(ctx, sessionID, fromEntryID, name, map[string]any{
			"created_by":      "crush_rlm_runtime",
			"from_branch_id":  activeID,
			"from_checkpoint": activeBranch.HeadCheckpoint,
		})
		if forkErr != nil {
			return BranchState{}, forkErr
		}
		if forked != nil && forked.ID != "" {
			branchID = forked.ID
		}
	}

	if strings.TrimSpace(name) == "" {
		name = "branch-" + branchID[:8]
	}

	branch := BranchState{
		BranchID:       branchID,
		ParentBranchID: activeID,
		Status:         BranchStatusActive,
		HeadCheckpoint: activeBranch.HeadCheckpoint,
		JournalOffset:  activeBranch.JournalOffset,
		BranchLocal:    maps.Clone(activeBranch.BranchLocal),
	}
	if activeBranch.HeadCheckpoint != "" {
		_, state, completion, replay, err := m.store.LoadCheckpoint(ctx, sessionID, activeID, activeBranch.HeadCheckpoint)
		if err != nil {
			return BranchState{}, err
		}
		checkpoint, err := m.store.WriteCheckpoint(ctx, sessionID, branch, state, replay, completion)
		if err != nil {
			return BranchState{}, err
		}
		branch.HeadCheckpoint = checkpoint.ID
	}

	if err := m.store.SaveBranch(ctx, sessionID, branch); err != nil {
		return BranchState{}, err
	}
	if branch.HeadCheckpoint != "" {
		if err := m.store.PromoteBranchHead(ctx, sessionID, branchID, branch.HeadCheckpoint); err != nil {
			return BranchState{}, err
		}
	}
	runtime.Branches[branchID] = branch

	if _, err := m.store.AppendJournal(ctx, sessionID, activeID, JournalEntry{
		Timestamp: time.Now().UTC(),
		Kind:      "branch_fork",
		Payload: map[string]any{
			"new_branch_id": branchID,
			"name":          name,
		},
	}); err == nil {
		activeBranch.JournalOffset++
		runtime.Branches[activeID] = activeBranch
	}

	if err := m.SaveSession(ctx, runtime); err != nil {
		return BranchState{}, err
	}
	return branch, nil
}

func (m *Manager) SwitchBranch(ctx context.Context, sessionID, branchID string) error {
	runtime, err := m.LoadOrCreate(ctx, sessionID)
	if err != nil {
		return err
	}

	branch, err := m.store.LoadBranch(ctx, sessionID, branchID)
	if err != nil {
		return err
	}
	runtime.Branches[branchID] = branch
	runtime.State.ActiveBranchID = branchID

	if m.sessionEvents != nil {
		if err := m.sessionEvents.SetActiveBranch(ctx, sessionID, branchID); err != nil {
			return err
		}
	}

	if err := m.store.SaveRuntime(ctx, runtime.State); err != nil {
		return err
	}
	if runtime.REPL != nil {
		if _, err := m.ensureRuntimeBridge(ctx, runtime, ""); err != nil {
			return err
		}
		if err := m.restoreActiveBranch(ctx, runtime); err != nil {
			return err
		}
	}

	_, _ = m.store.AppendJournal(ctx, sessionID, branchID, JournalEntry{
		Timestamp: time.Now().UTC(),
		Kind:      "branch_switch",
		Payload: map[string]any{
			"active_branch_id": branchID,
		},
	})

	return nil
}

func (m *Manager) ResumeBranch(ctx context.Context, sessionID, branchID string) error {
	return m.SwitchBranch(ctx, sessionID, branchID)
}

func (m *Manager) InspectBranches(ctx context.Context, sessionID string) ([]BranchState, error) {
	return m.store.ListBranches(ctx, sessionID)
}

func (m *Manager) ensureForkEntry(ctx context.Context, sessionID, branchID string) (string, error) {
	if m.sessionEvents == nil {
		return "", fmt.Errorf("sessionevent store unavailable")
	}

	head, err := m.sessionEvents.GetBranchHead(ctx, sessionID, branchID)
	if err == nil && head != nil && head.ID != "" {
		return head.ID, nil
	}

	entries, err := m.sessionEvents.AppendEntries(ctx, []sessionevent.SessionEntry{{
		SessionID: sessionID,
		BranchID:  branchID,
		Kind:      sessionevent.EntryKindSystemEvent,
		Role:      "system",
		Payload: map[string]any{
			"event": "branch_fork_origin",
		},
	}})
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("failed to create fork origin entry")
	}
	return entries[0].ID, nil
}
