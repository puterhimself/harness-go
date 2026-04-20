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

type RLMRuntimeTokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type RLMRuntimeRootSnapshot struct {
	Iteration        int
	PromptTokens     int
	CompletionTokens int
}

type RLMRuntimeTraceStep struct {
	Index       int
	Thought     string
	Action      string
	Code        string
	SubQuery    string
	Observation string
	Duration    time.Duration
	Success     bool
	Error       string
}

type RLMRuntimeTrace struct {
	StartedAt         time.Time
	CompletedAt       time.Time
	ProcessingTime    time.Duration
	Iterations        int
	Usage             RLMRuntimeTokenUsage
	RootUsage         RLMRuntimeTokenUsage
	SubUsage          RLMRuntimeTokenUsage
	SubRLMUsage       RLMRuntimeTokenUsage
	RootSnapshots     []RLMRuntimeRootSnapshot
	SubLLMCallCount   int
	SubRLMCallCount   int
	ConfidenceSignals int
	CompressionCount  int
	TerminationCause  string
	Error             string
	Steps             []RLMRuntimeTraceStep
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
	ActiveTrace   RLMRuntimeTrace
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
	cloned.ActiveTrace = cloneRLMRuntimeTrace(r.ActiveTrace)
	return cloned
}

func cloneRLMRuntimeTrace(trace RLMRuntimeTrace) RLMRuntimeTrace {
	cloned := trace
	cloned.RootSnapshots = append([]RLMRuntimeRootSnapshot(nil), trace.RootSnapshots...)
	cloned.Steps = append([]RLMRuntimeTraceStep(nil), trace.Steps...)
	return cloned
}

type RLMRuntime interface {
	InspectRuntime(ctx context.Context, sessionID string, journalLimit int) (RLMRuntimeInspection, error)
	ForkRuntimeBranch(ctx context.Context, sessionID, name string) (RLMRuntimeBranch, error)
	SwitchRuntimeBranch(ctx context.Context, sessionID, branchID string) error
	ResumeRuntimeBranch(ctx context.Context, sessionID, branchID string) error
}
