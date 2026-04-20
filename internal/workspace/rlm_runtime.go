package workspace

import (
	"context"
	"errors"
	"maps"
	"time"
)

var ErrRLMRuntimeUnavailable = errors.New("rlm runtime inspection unavailable")

type RLMRuntimeBranch struct {
	BranchID       string
	ParentBranchID string
	Status         string
	Summary        string
	HeadCheckpoint string
	JournalOffset  int64
}

type RLMRuntimePublish struct {
	ID             string
	WorkerBranchID string
	Summary        string
	CreatedAt      time.Time
}

type RLMRuntimeJournalEntry struct {
	Timestamp time.Time
	Kind      string
	Payload   map[string]any
}

type RLMRuntimeInspection struct {
	SessionID         string
	ActiveBranchID    string
	Done              bool
	OutputMessage     string
	OutputData        map[string]any
	ArtifactCount     int
	MessageCount      int
	TerminationCause  string
	EpisodeStartedAt  time.Time
	EpisodeFinishedAt time.Time
	WorkerLimit       int
	WorkerUsage       int
	RecoveredFromDisk bool

	Branches      []RLMRuntimeBranch
	Published     []RLMRuntimePublish
	RecentJournal []RLMRuntimeJournalEntry
}

func (r RLMRuntimeInspection) Clone() RLMRuntimeInspection {
	cloned := r
	cloned.OutputData = maps.Clone(r.OutputData)
	cloned.Branches = append([]RLMRuntimeBranch(nil), r.Branches...)
	cloned.Published = append([]RLMRuntimePublish(nil), r.Published...)
	cloned.RecentJournal = make([]RLMRuntimeJournalEntry, 0, len(r.RecentJournal))
	for _, entry := range r.RecentJournal {
		cloned.RecentJournal = append(cloned.RecentJournal, RLMRuntimeJournalEntry{
			Timestamp: entry.Timestamp,
			Kind:      entry.Kind,
			Payload:   maps.Clone(entry.Payload),
		})
	}
	return cloned
}

type RLMRuntime interface {
	InspectRuntime(ctx context.Context, sessionID string, journalLimit int) (RLMRuntimeInspection, error)
	ForkRuntimeBranch(ctx context.Context, sessionID, name string) (RLMRuntimeBranch, error)
	SwitchRuntimeBranch(ctx context.Context, sessionID, branchID string) error
	ResumeRuntimeBranch(ctx context.Context, sessionID, branchID string) error
}
