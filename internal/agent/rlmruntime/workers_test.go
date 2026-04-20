package rlmruntime

import (
	"context"
	"testing"

	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/stretchr/testify/require"
)

func TestRunWorkerFromCheckpointIsolationAndCommit(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetSessionEventStore(newFakeSessionEventStore())
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	ctx := context.Background()
	runtime, err := manager.LoadOrCreate(ctx, "session-1")
	require.NoError(t, err)

	runtime.State.Shared = map[string]any{"root": "keep"}
	require.NoError(t, runtime.REPL.ImportState(map[string]any{
		"shared":       runtime.State.Shared,
		"branch_local": map[string]any{"branch": "root"},
		"done":         false,
	}))

	cp, err := manager.CheckpointActive(ctx, runtime, []string{"shared := map[string]any{\"root\": \"keep\"}"}, NormalizedCompletion{}, EpisodeTrace{})
	require.NoError(t, err)

	published, err := manager.RunWorkerFromCheckpoint(ctx, "session-1", runtime.State.ActiveBranchID, cp.ID, func(ctx context.Context, worker *WorkerRuntime) (PublishPayload, error) {
		workerREPL, ok := worker.REPL.(*rlm.YaegiREPL)
		require.True(t, ok)
		require.NoError(t, workerREPL.SetValue("shared", map[string]any{"root": "mutated"}))
		return PublishPayload{
			Summary:       "worker completed",
			SharedUpdates: map[string]any{"worker": "done"},
		}, nil
	})
	require.NoError(t, err)

	require.Equal(t, "keep", runtime.State.Shared["root"])
	require.Equal(t, 1, len(manager.ListPublished("session-1")))

	require.NoError(t, manager.Commit(ctx, "session-1", published.ID))
	require.Equal(t, "keep", runtime.State.Shared["root"])
	require.Equal(t, "done", runtime.State.Shared["worker"])
}

func TestInspectRuntimeAndJournal(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	ctx := context.Background()
	runtime, err := manager.LoadOrCreate(ctx, "session-1")
	require.NoError(t, err)
	require.NoError(t, runtime.REPL.ImportState(map[string]any{"done": true, "output_message": "done"}))
	_, err = manager.CheckpointActive(ctx, runtime, []string{"done = true"}, NormalizedCompletion{Done: true, OutputMessage: "done"}, EpisodeTrace{
		Iterations:       1,
		TerminationCause: "state_done",
		Steps:            []EpisodeTraceStep{{Index: 1, Action: "final", Observation: "done", Success: true}},
	})
	require.NoError(t, err)

	_, err = manager.Publish(ctx, "session-1", PublishPayload{Summary: "pending"})
	require.NoError(t, err)

	inspection, err := manager.InspectRuntime(ctx, "session-1")
	require.NoError(t, err)
	require.Equal(t, runtime.SessionID, inspection.Runtime.SessionID)
	require.NotEmpty(t, inspection.Published)
	require.Equal(t, "state_done", inspection.ActiveTrace.TerminationCause)
	require.Len(t, inspection.ActiveTrace.Steps, 1)

	entries, err := manager.InspectBranchJournal(ctx, "session-1", runtime.State.ActiveBranchID, 20)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
}
