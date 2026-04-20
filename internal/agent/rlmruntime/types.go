package rlmruntime

import "time"

const (
	RuntimeSchemaVersion = "v1"

	BranchStatusActive   = "active"
	BranchStatusArchived = "archived"
)

type ArtifactRef struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Path      string         `json:"path"`
	CreatedAt time.Time      `json:"created_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type RuntimeMessage struct {
	ID        string         `json:"id"`
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Timestamp time.Time      `json:"timestamp"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type EpisodeState struct {
	EpisodeID        string    `json:"episode_id"`
	UserMessageID    string    `json:"user_message_id"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at,omitempty"`
	TerminationCause string    `json:"termination_cause,omitempty"`
}

type RuntimeState struct {
	SessionID      string                 `json:"session_id"`
	ActiveBranchID string                 `json:"active_branch_id"`
	Messages       []RuntimeMessage       `json:"messages"`
	Shared         map[string]any         `json:"shared"`
	Done           bool                   `json:"done"`
	OutputMessage  string                 `json:"output_message"`
	OutputData     map[string]any         `json:"output_data"`
	Artifacts      map[string]ArtifactRef `json:"artifacts"`
	Episode        EpisodeState           `json:"episode"`
}

type BranchState struct {
	BranchID       string         `json:"branch_id"`
	ParentBranchID string         `json:"parent_branch_id,omitempty"`
	Status         string         `json:"status"`
	Summary        string         `json:"summary,omitempty"`
	HeadCheckpoint string         `json:"head_checkpoint"`
	JournalOffset  int64          `json:"journal_offset"`
	BranchLocal    map[string]any `json:"branch_local"`
}

type Checkpoint struct {
	ID           string    `json:"id"`
	BranchID     string    `json:"branch_id"`
	CreatedAt    time.Time `json:"created_at"`
	ManifestPath string    `json:"manifest_path"`
	StatePath    string    `json:"state_path"`
	ReplayPath   string    `json:"replay_path"`
	ArtifactRoot string    `json:"artifact_root"`
}

type CheckpointManifest struct {
	ID               string                 `json:"id"`
	Version          string                 `json:"version"`
	SessionID        string                 `json:"session_id"`
	BranchID         string                 `json:"branch_id"`
	CreatedAt        time.Time              `json:"created_at"`
	ReplayPath       string                 `json:"replay_path"`
	StatePath        string                 `json:"state_path"`
	CompletionPath   string                 `json:"completion_path"`
	ArtifactRoot     string                 `json:"artifact_root"`
	JournalOffset    int64                  `json:"journal_offset"`
	RuntimeVariables map[string]any         `json:"runtime_variables,omitempty"`
	Artifacts        map[string]ArtifactRef `json:"artifacts,omitempty"`
}

type BranchHead struct {
	BranchID      string    `json:"branch_id"`
	CheckpointID  string    `json:"checkpoint_id"`
	UpdatedAt     time.Time `json:"updated_at"`
	CheckpointDir string    `json:"checkpoint_dir"`
}

type JournalEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Kind      string         `json:"kind"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type PublishPayload struct {
	ID               string                 `json:"id"`
	WorkerBranchID   string                 `json:"worker_branch_id,omitempty"`
	Summary          string                 `json:"summary,omitempty"`
	OutputData       map[string]any         `json:"output_data,omitempty"`
	Artifacts        map[string]ArtifactRef `json:"artifacts,omitempty"`
	SharedUpdates    map[string]any         `json:"shared_updates,omitempty"`
	BranchLocalDelta map[string]any         `json:"branch_local_delta,omitempty"`
	CreatedAt        time.Time              `json:"created_at"`
}

type WorkerRuntime struct {
	SessionID  string
	BranchID   string
	Checkpoint string
	REPL       any
	State      map[string]any
}

type NormalizedCompletion struct {
	Done          bool                   `json:"done"`
	OutputMessage string                 `json:"output_message"`
	OutputData    map[string]any         `json:"output_data,omitempty"`
	Artifacts     map[string]ArtifactRef `json:"artifacts,omitempty"`
	BranchSummary string                 `json:"branch_summary,omitempty"`
	Reason        string                 `json:"reason,omitempty"`
}
