package rlmruntime

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestManagerLoadOrCreateAndRecover(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetSessionEventStore(newFakeSessionEventStore())
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	ctx := context.Background()
	runtime, err := manager.LoadOrCreate(ctx, "session-1")
	require.NoError(t, err)
	require.NotEmpty(t, runtime.State.ActiveBranchID)
	require.NotNil(t, runtime.REPL)

	runtime.State.Shared = map[string]any{"count": float64(2)}
	require.NoError(t, manager.SaveSession(ctx, runtime))

	manager.UnloadSession("session-1")
	recovered, err := manager.RecoverSession(ctx, "session-1")
	require.NoError(t, err)
	require.Equal(t, float64(2), recovered.State.Shared["count"])
}

func TestManagerCheckpointAndRestoreBranchState(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetSessionEventStore(newFakeSessionEventStore())
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	ctx := context.Background()
	runtime, err := manager.LoadOrCreate(ctx, "session-1")
	require.NoError(t, err)

	require.NoError(t, runtime.REPL.ImportState(map[string]any{
		"task":           "checkpoint",
		"shared":         map[string]any{"k": "v"},
		"branch_local":   map[string]any{"branch": "main"},
		"done":           true,
		"output_message": "finished",
		"output_data":    map[string]any{"ok": true},
	}))

	cp, err := manager.CheckpointActive(ctx, runtime, []string{"done = true"}, NormalizedCompletion{Done: true, OutputMessage: "finished"}, EpisodeTrace{})
	require.NoError(t, err)
	require.NotEmpty(t, cp.ID)

	manager.UnloadSession("session-1")
	recovered, err := manager.RecoverSession(ctx, "session-1")
	require.NoError(t, err)

	value, err := recovered.REPL.GetValue("output_message")
	require.NoError(t, err)
	require.Equal(t, "finished", value)
}

func TestManagerRecoverSessionInjectsBridgeBeforeReplay(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetSessionEventStore(newFakeSessionEventStore())
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	var calls int
	tool := &fakeTool{name: "restore_tool", run: func(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
		calls++
		return fantasy.NewTextResponse("ok"), nil
	}}
	manager.SetBridgeProvider(func(ctx context.Context, sessionID string) (message.Service, []fantasy.AgentTool, error) {
		return nil, []fantasy.AgentTool{tool}, nil
	})

	ctx := context.Background()
	runtime, err := manager.LoadOrCreate(ctx, "session-1")
	require.NoError(t, err)

	_, err = runtime.REPL.Execute(ctx, `ToolCall("restore_tool", map[string]any{"phase": "original"})`)
	require.NoError(t, err)

	_, err = manager.CheckpointActive(ctx, runtime, []string{`ToolCall("restore_tool", map[string]any{"phase": "replay"})`}, NormalizedCompletion{}, EpisodeTrace{})
	require.NoError(t, err)
	require.Equal(t, 1, calls)

	manager.UnloadSession("session-1")
	_, err = manager.RecoverSession(ctx, "session-1")
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

func TestManagerForkAndSwitchBranch(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetSessionEventStore(newFakeSessionEventStore())
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	ctx := context.Background()
	runtime, err := manager.LoadOrCreate(ctx, "session-1")
	require.NoError(t, err)

	require.NoError(t, runtime.REPL.ImportState(map[string]any{
		"branch_local": map[string]any{"x": "main"},
	}))
	_, err = manager.CheckpointActive(ctx, runtime, []string{"x := \"main\""}, NormalizedCompletion{Done: false}, EpisodeTrace{})
	require.NoError(t, err)

	branch, err := manager.ForkBranch(ctx, "session-1", "experiment")
	require.NoError(t, err)
	require.NotEmpty(t, branch.BranchID)
	require.NotEqual(t, runtime.State.ActiveBranchID, branch.BranchID)
	require.NotEmpty(t, branch.HeadCheckpoint)

	require.NoError(t, manager.SwitchBranch(ctx, "session-1", branch.BranchID))
	require.Equal(t, branch.BranchID, runtime.State.ActiveBranchID)

	value, err := runtime.REPL.GetValue("branch_local")
	require.NoError(t, err)
	branchLocal, ok := value.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "main", branchLocal["x"])

	result, err := runtime.REPL.Execute(ctx, `fmt.Println(x)`)
	require.NoError(t, err)
	require.Contains(t, result.Stdout, "main")
}

func TestManagerSwitchBranchResetsREPLState(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetSessionEventStore(newFakeSessionEventStore())
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	ctx := context.Background()
	runtime, err := manager.LoadOrCreate(ctx, "session-1")
	require.NoError(t, err)

	_, err = runtime.REPL.Execute(ctx, `func mainOnly() string { return "main" }`)
	require.NoError(t, err)
	_, err = manager.CheckpointActive(ctx, runtime, []string{`func mainOnly() string { return "main" }`}, NormalizedCompletion{}, EpisodeTrace{})
	require.NoError(t, err)

	branch, err := manager.ForkBranch(ctx, "session-1", "child")
	require.NoError(t, err)
	require.NoError(t, manager.SwitchBranch(ctx, "session-1", branch.BranchID))

	_, err = runtime.REPL.Execute(ctx, `func childOnly() string { return "child" }`)
	require.NoError(t, err)
	_, err = manager.CheckpointActive(ctx, runtime, []string{`func mainOnly() string { return "main" }`, `func childOnly() string { return "child" }`}, NormalizedCompletion{}, EpisodeTrace{})
	require.NoError(t, err)

	mainBranchID := runtime.Branches[branch.BranchID].ParentBranchID
	require.NoError(t, manager.SwitchBranch(ctx, "session-1", mainBranchID))

	_, err = runtime.REPL.GetValue("childOnly")
	require.Error(t, err)

	result, err := runtime.REPL.Execute(ctx, `fmt.Println(mainOnly())`)
	require.NoError(t, err)
	require.Contains(t, result.Stdout, "main")
}

type fakeSessionEventStore struct {
	sessions map[string]*sessionevent.Session
	branches map[string]map[string]*sessionevent.SessionBranch
	entries  map[string]map[string][]sessionevent.SessionEntry
}

func newFakeSessionEventStore() *fakeSessionEventStore {
	return &fakeSessionEventStore{
		sessions: map[string]*sessionevent.Session{},
		branches: map[string]map[string]*sessionevent.SessionBranch{},
		entries:  map[string]map[string][]sessionevent.SessionEntry{},
	}
}

func (f *fakeSessionEventStore) CreateSession(ctx context.Context, params sessionevent.CreateSessionParams) (*sessionevent.Session, *sessionevent.SessionBranch, error) {
	branchID := uuid.NewString()
	now := nowUTC()
	session := &sessionevent.Session{
		ID:             params.ID,
		Title:          params.Title,
		Status:         sessionevent.SessionStatusActive,
		ActiveBranchID: branchID,
		CreatedAt:      now,
		UpdatedAt:      now,
		Metadata:       maps.Clone(params.Metadata),
	}
	branch := &sessionevent.SessionBranch{
		ID:        branchID,
		SessionID: params.ID,
		Name:      "main",
		Status:    sessionevent.BranchStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  map[string]any{},
	}
	f.sessions[params.ID] = session
	if _, ok := f.branches[params.ID]; !ok {
		f.branches[params.ID] = map[string]*sessionevent.SessionBranch{}
	}
	f.branches[params.ID][branchID] = branch
	if _, ok := f.entries[params.ID]; !ok {
		f.entries[params.ID] = map[string][]sessionevent.SessionEntry{}
	}
	f.entries[params.ID][branchID] = []sessionevent.SessionEntry{}
	return session, branch, nil
}

func (f *fakeSessionEventStore) AppendEntries(ctx context.Context, entries []sessionevent.SessionEntry) ([]sessionevent.SessionEntry, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]sessionevent.SessionEntry, len(entries))
	for i, entry := range entries {
		entry.ID = uuid.NewString()
		if entry.CreatedAt.IsZero() {
			entry.CreatedAt = nowUTC()
		}
		if _, ok := f.entries[entry.SessionID]; !ok {
			f.entries[entry.SessionID] = map[string][]sessionevent.SessionEntry{}
		}
		f.entries[entry.SessionID][entry.BranchID] = append(f.entries[entry.SessionID][entry.BranchID], entry)
		branch := f.branches[entry.SessionID][entry.BranchID]
		if branch != nil {
			branch.HeadEntryID = entry.ID
			branch.UpdatedAt = nowUTC()
		}
		out[i] = entry
	}
	return out, nil
}

func (f *fakeSessionEventStore) AppendSummary(ctx context.Context, summary sessionevent.SessionSummary) error {
	return nil
}

func (f *fakeSessionEventStore) SetActiveBranch(ctx context.Context, sessionID, branchID string) error {
	session, ok := f.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found")
	}
	session.ActiveBranchID = branchID
	session.UpdatedAt = nowUTC()
	return nil
}

func (f *fakeSessionEventStore) ForkBranch(ctx context.Context, sessionID, fromEntryID, name string, metadata map[string]any) (*sessionevent.SessionBranch, error) {
	if _, ok := f.sessions[sessionID]; !ok {
		return nil, fmt.Errorf("session not found")
	}
	branchID := uuid.NewString()
	branch := &sessionevent.SessionBranch{
		ID:            branchID,
		SessionID:     sessionID,
		Name:          name,
		OriginEntryID: fromEntryID,
		HeadEntryID:   fromEntryID,
		Status:        sessionevent.BranchStatusActive,
		CreatedAt:     nowUTC(),
		UpdatedAt:     nowUTC(),
		Metadata:      maps.Clone(metadata),
	}
	if _, ok := f.branches[sessionID]; !ok {
		f.branches[sessionID] = map[string]*sessionevent.SessionBranch{}
	}
	f.branches[sessionID][branchID] = branch
	if _, ok := f.entries[sessionID]; !ok {
		f.entries[sessionID] = map[string][]sessionevent.SessionEntry{}
	}
	f.entries[sessionID][branchID] = []sessionevent.SessionEntry{}
	return branch, nil
}

func (f *fakeSessionEventStore) GetSession(ctx context.Context, sessionID string) (*sessionevent.Session, error) {
	session, ok := f.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	clone := *session
	clone.Metadata = maps.Clone(session.Metadata)
	return &clone, nil
}

func (f *fakeSessionEventStore) ListSessions(ctx context.Context) ([]sessionevent.Session, error) {
	out := make([]sessionevent.Session, 0, len(f.sessions))
	for _, session := range f.sessions {
		clone := *session
		clone.Metadata = maps.Clone(session.Metadata)
		out = append(out, clone)
	}
	return out, nil
}

func (f *fakeSessionEventStore) ListBranches(ctx context.Context, sessionID string) ([]sessionevent.SessionBranch, error) {
	branches := f.branches[sessionID]
	out := make([]sessionevent.SessionBranch, 0, len(branches))
	for _, branch := range branches {
		clone := *branch
		clone.Metadata = maps.Clone(branch.Metadata)
		out = append(out, clone)
	}
	return out, nil
}

func (f *fakeSessionEventStore) GetEntry(ctx context.Context, sessionID, entryID string) (*sessionevent.SessionEntry, error) {
	for _, entries := range f.entries[sessionID] {
		for _, entry := range entries {
			if entry.ID == entryID {
				clone := entry
				clone.Payload = maps.Clone(entry.Payload)
				clone.Metadata = maps.Clone(entry.Metadata)
				return &clone, nil
			}
		}
	}
	return nil, fmt.Errorf("entry not found")
}

func (f *fakeSessionEventStore) GetBranchHead(ctx context.Context, sessionID, branchID string) (*sessionevent.SessionEntry, error) {
	branch := f.branches[sessionID][branchID]
	if branch == nil || branch.HeadEntryID == "" {
		return nil, nil
	}
	return f.GetEntry(ctx, sessionID, branch.HeadEntryID)
}

func (f *fakeSessionEventStore) LoadLineage(ctx context.Context, sessionID, headEntryID string, opts sessionevent.LoadOptions) ([]sessionevent.SessionEntry, error) {
	entry, err := f.GetEntry(ctx, sessionID, headEntryID)
	if err != nil {
		return nil, err
	}
	return []sessionevent.SessionEntry{*entry}, nil
}

func (f *fakeSessionEventStore) LoadSummaries(ctx context.Context, sessionID, branchID string, limit int) ([]sessionevent.SessionSummary, error) {
	return nil, nil
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
