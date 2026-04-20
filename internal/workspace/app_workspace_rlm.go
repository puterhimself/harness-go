package workspace

import (
	"context"
	"errors"
	"maps"

	"github.com/charmbracelet/crush/internal/agent/rlmruntime"
)

type rlmRuntimeCoordinator interface {
	InspectRuntime(ctx context.Context, sessionID string) (rlmruntime.RuntimeInspection, error)
	InspectBranchJournal(ctx context.Context, sessionID, branchID string, limit int) ([]rlmruntime.JournalEntry, error)
	ForkBranch(ctx context.Context, sessionID, name string) (rlmruntime.BranchState, error)
	SwitchBranch(ctx context.Context, sessionID, branchID string) error
	ResumeBranch(ctx context.Context, sessionID, branchID string) error
	WorkerLimit() int
	WorkerUsage() int
}

func (w *AppWorkspace) InspectRuntime(ctx context.Context, sessionID string, journalLimit int) (RLMRuntimeInspection, error) {
	coordinator, err := w.rlmCoordinator()
	if err != nil {
		return RLMRuntimeInspection{}, err
	}

	inspection, err := coordinator.InspectRuntime(ctx, sessionID)
	if err != nil {
		return RLMRuntimeInspection{}, err
	}

	entries, err := coordinator.InspectBranchJournal(ctx, sessionID, inspection.Runtime.ActiveBranchID, journalLimit)
	if err != nil {
		return RLMRuntimeInspection{}, err
	}

	recent := make([]RLMRuntimeJournalEntry, 0, len(entries))
	recovered := false
	for _, entry := range entries {
		recent = append(recent, RLMRuntimeJournalEntry{
			Timestamp: entry.Timestamp,
			Kind:      entry.Kind,
			Payload:   maps.Clone(entry.Payload),
		})
		if entry.Kind == "recovery_rehydrated" {
			recovered = true
		}
	}

	branches := make([]RLMRuntimeBranch, 0, len(inspection.Branches))
	for _, branch := range inspection.Branches {
		branches = append(branches, RLMRuntimeBranch{
			BranchID:       branch.BranchID,
			ParentBranchID: branch.ParentBranchID,
			Status:         branch.Status,
			Summary:        branch.Summary,
			HeadCheckpoint: branch.HeadCheckpoint,
			JournalOffset:  branch.JournalOffset,
		})
	}

	published := make([]RLMRuntimePublish, 0, len(inspection.Published))
	for _, item := range inspection.Published {
		published = append(published, RLMRuntimePublish{
			ID:             item.ID,
			WorkerBranchID: item.WorkerBranchID,
			Summary:        item.Summary,
			CreatedAt:      item.CreatedAt,
		})
	}

	return RLMRuntimeInspection{
		SessionID:         sessionID,
		ActiveBranchID:    inspection.Runtime.ActiveBranchID,
		Done:              inspection.Runtime.Done,
		OutputMessage:     inspection.Runtime.OutputMessage,
		OutputData:        maps.Clone(inspection.Runtime.OutputData),
		ArtifactCount:     len(inspection.Runtime.Artifacts),
		MessageCount:      len(inspection.Runtime.Messages),
		TerminationCause:  inspection.Runtime.Episode.TerminationCause,
		EpisodeStartedAt:  inspection.Runtime.Episode.StartedAt,
		EpisodeFinishedAt: inspection.Runtime.Episode.FinishedAt,
		WorkerLimit:       coordinator.WorkerLimit(),
		WorkerUsage:       coordinator.WorkerUsage(),
		RecoveredFromDisk: recovered,
		Branches:          branches,
		Published:         published,
		RecentJournal:     recent,
	}, nil
}

func (w *AppWorkspace) ForkRuntimeBranch(ctx context.Context, sessionID, name string) (RLMRuntimeBranch, error) {
	coordinator, err := w.rlmCoordinator()
	if err != nil {
		return RLMRuntimeBranch{}, err
	}

	branch, err := coordinator.ForkBranch(ctx, sessionID, name)
	if err != nil {
		return RLMRuntimeBranch{}, err
	}

	return RLMRuntimeBranch{
		BranchID:       branch.BranchID,
		ParentBranchID: branch.ParentBranchID,
		Status:         branch.Status,
		Summary:        branch.Summary,
		HeadCheckpoint: branch.HeadCheckpoint,
		JournalOffset:  branch.JournalOffset,
	}, nil
}

func (w *AppWorkspace) SwitchRuntimeBranch(ctx context.Context, sessionID, branchID string) error {
	coordinator, err := w.rlmCoordinator()
	if err != nil {
		return err
	}
	return coordinator.SwitchBranch(ctx, sessionID, branchID)
}

func (w *AppWorkspace) ResumeRuntimeBranch(ctx context.Context, sessionID, branchID string) error {
	coordinator, err := w.rlmCoordinator()
	if err != nil {
		return err
	}
	return coordinator.ResumeBranch(ctx, sessionID, branchID)
}

func (w *AppWorkspace) rlmCoordinator() (rlmRuntimeCoordinator, error) {
	if w.app == nil || w.app.AgentCoordinator == nil {
		return nil, ErrRLMRuntimeUnavailable
	}
	coordinator, ok := w.app.AgentCoordinator.(rlmRuntimeCoordinator)
	if !ok {
		return nil, ErrRLMRuntimeUnavailable
	}
	if coordinator == nil {
		return nil, ErrRLMRuntimeUnavailable
	}
	return coordinator, nil
}

var _ RLMRuntime = (*AppWorkspace)(nil)

func IsRLMRuntimeUnavailable(err error) bool {
	return errors.Is(err, ErrRLMRuntimeUnavailable)
}
