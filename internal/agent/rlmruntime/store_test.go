package rlmruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileStoreSaveLoadRuntime(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	runtime := RuntimeState{
		SessionID:      "session-1",
		ActiveBranchID: "main",
		Shared:         map[string]any{"count": float64(1)},
		OutputData:     map[string]any{"status": "ok"},
	}

	require.NoError(t, store.SaveRuntime(context.Background(), runtime))

	loaded, err := store.LoadRuntime(context.Background(), "session-1")
	require.NoError(t, err)
	require.Equal(t, runtime.SessionID, loaded.SessionID)
	require.Equal(t, runtime.ActiveBranchID, loaded.ActiveBranchID)
	require.Equal(t, runtime.Shared, loaded.Shared)
}

func TestFileStoreSaveLoadBranchAndList(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	ctx := context.Background()

	branchA := BranchState{BranchID: "a", Status: BranchStatusActive}
	branchB := BranchState{BranchID: "b", Status: BranchStatusActive}

	require.NoError(t, store.SaveBranch(ctx, "session-1", branchB))
	require.NoError(t, store.SaveBranch(ctx, "session-1", branchA))

	loaded, err := store.LoadBranch(ctx, "session-1", "a")
	require.NoError(t, err)
	require.Equal(t, "a", loaded.BranchID)

	branches, err := store.ListBranches(ctx, "session-1")
	require.NoError(t, err)
	require.Len(t, branches, 2)
	require.Equal(t, "a", branches[0].BranchID)
	require.Equal(t, "b", branches[1].BranchID)
}

func TestFileStoreCheckpointWriteLoadPromote(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	ctx := context.Background()

	branch := BranchState{
		BranchID:      "main",
		Status:        BranchStatusActive,
		JournalOffset: 2,
	}
	require.NoError(t, store.SaveBranch(ctx, "session-1", branch))

	trace := EpisodeTrace{Iterations: 2, TerminationCause: "state_done", Steps: []EpisodeTraceStep{{Index: 1, Action: "compute", Code: "x := 1"}}}
	cp, err := store.WriteCheckpoint(ctx, "session-1", branch, map[string]any{
		"task": "ship store",
		"done": true,
	}, []string{"x := 1", "done = true"}, NormalizedCompletion{Done: true, OutputMessage: "done"}, trace)
	require.NoError(t, err)
	require.NotEmpty(t, cp.ID)

	manifest, state, completion, replay, err := store.LoadCheckpoint(ctx, "session-1", "main", cp.ID)
	require.NoError(t, err)
	require.Equal(t, cp.ID, manifest.ID)
	require.Equal(t, RuntimeSchemaVersion, manifest.Version)
	require.Equal(t, true, state["done"])
	require.Equal(t, "done", completion.OutputMessage)
	require.Equal(t, []string{"x := 1", "done = true"}, replay)

	loadedTrace, err := store.LoadCheckpointTrace(ctx, "session-1", "main", cp.ID)
	require.NoError(t, err)
	require.Equal(t, trace.TerminationCause, loadedTrace.TerminationCause)
	require.Len(t, loadedTrace.Steps, 1)

	require.NoError(t, store.PromoteBranchHead(ctx, "session-1", "main", cp.ID))
	head, err := store.LoadBranchHead(ctx, "session-1", "main")
	require.NoError(t, err)
	require.Equal(t, cp.ID, head.CheckpointID)

	updatedBranch, err := store.LoadBranch(ctx, "session-1", "main")
	require.NoError(t, err)
	require.Equal(t, cp.ID, updatedBranch.HeadCheckpoint)
}

func TestFileStoreCheckpointPreservesMultilineReplayBlocks(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	ctx := context.Background()

	branch := BranchState{BranchID: "main", Status: BranchStatusActive}
	require.NoError(t, store.SaveBranch(ctx, "session-1", branch))

	replay := []string{
		"func counter() int {\n\ttotal := 0\n\tfor i := 0; i < 3; i++ {\n\t\ttotal += i\n\t}\n\treturn total\n}",
		"value := counter()",
	}
	cp, err := store.WriteCheckpoint(ctx, "session-1", branch, map[string]any{"done": false}, replay, NormalizedCompletion{}, EpisodeTrace{})
	require.NoError(t, err)

	_, _, _, loadedReplay, err := store.LoadCheckpoint(ctx, "session-1", "main", cp.ID)
	require.NoError(t, err)
	require.Equal(t, replay, loadedReplay)
}

func TestFileStoreAppendJournalOffsets(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	ctx := context.Background()

	offset, err := store.AppendJournal(ctx, "session-1", "main", JournalEntry{Kind: "tool_call"})
	require.NoError(t, err)
	require.Equal(t, int64(1), offset)

	offset, err = store.AppendJournal(ctx, "session-1", "main", JournalEntry{Kind: "tool_result"})
	require.NoError(t, err)
	require.Equal(t, int64(2), offset)
}

func TestFileStorePutArtifact(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	ctx := context.Background()

	artifact, err := store.PutArtifact(ctx, "session-1", "main", "summary", map[string]any{"text": "hello"})
	require.NoError(t, err)
	require.NotEmpty(t, artifact.ID)
	require.Equal(t, "summary", artifact.Name)

	_, err = os.Stat(artifact.Path)
	require.NoError(t, err)
}

func TestFileStoreAtomicReplacementLeavesNoTempFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := NewFileStore(root)
	ctx := context.Background()

	runtime := RuntimeState{SessionID: "session-1", ActiveBranchID: "main"}
	require.NoError(t, store.SaveRuntime(ctx, runtime))

	runtime.Done = true
	runtime.OutputMessage = "updated"
	runtime.Episode = EpisodeState{EpisodeID: "ep-1", StartedAt: time.Now().UTC()}
	require.NoError(t, store.SaveRuntime(ctx, runtime))

	runtimePath := filepath.Join(root, "sessions", "session-1", "runtime.json")
	data, err := os.ReadFile(runtimePath)
	require.NoError(t, err)
	require.Contains(t, string(data), "\"updated\"")

	entries, err := os.ReadDir(filepath.Dir(runtimePath))
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), ".tmp-")
	}
}
