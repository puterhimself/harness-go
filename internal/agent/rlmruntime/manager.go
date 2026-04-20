package rlmruntime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/google/uuid"
)

type REPLFactory func() (*rlm.YaegiREPL, error)
type BridgeProvider func(ctx context.Context, sessionID string) (message.Service, []fantasy.AgentTool, error)

type SessionRuntime struct {
	SessionID      string
	State          RuntimeState
	Branches       map[string]BranchState
	REPL           *rlm.YaegiREPL
	bridge         *HostBridge
	bridgeInjected bool

	episodeMu sync.Mutex
	cancelMu  sync.Mutex
	cancel    context.CancelFunc
	dirtyMu   sync.Mutex
	dirtyVars map[string]struct{}
}

func (r *SessionRuntime) setCancel(cancel context.CancelFunc) {
	r.cancelMu.Lock()
	defer r.cancelMu.Unlock()
	r.cancel = cancel
}

func (r *SessionRuntime) clearCancel() {
	r.cancelMu.Lock()
	defer r.cancelMu.Unlock()
	r.cancel = nil
}

func (r *SessionRuntime) cancelEpisode() {
	r.cancelMu.Lock()
	defer r.cancelMu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *SessionRuntime) markDirty(names ...string) {
	r.dirtyMu.Lock()
	defer r.dirtyMu.Unlock()
	if r.dirtyVars == nil {
		r.dirtyVars = map[string]struct{}{}
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		r.dirtyVars[name] = struct{}{}
	}
}

func (r *SessionRuntime) clearDirty() {
	r.dirtyMu.Lock()
	defer r.dirtyMu.Unlock()
	clear(r.dirtyVars)
}

func (r *SessionRuntime) dirtySet() map[string]struct{} {
	r.dirtyMu.Lock()
	defer r.dirtyMu.Unlock()
	if len(r.dirtyVars) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(r.dirtyVars))
	for name := range r.dirtyVars {
		out[name] = struct{}{}
	}
	return out
}

type Manager struct {
	store         Store
	sessionEvents sessionevent.SessionEventStore
	replFactory   REPLFactory
	workerLimit   int
	workerSem     chan struct{}
	published     map[string]map[string]PublishPayload
	bridgeFactory BridgeProvider

	mu       sync.Mutex
	runtimes map[string]*SessionRuntime
}

func NewManager(store Store) *Manager {
	return &Manager{
		store:       store,
		runtimes:    make(map[string]*SessionRuntime),
		workerLimit: 2,
		workerSem:   make(chan struct{}, 2),
		published:   make(map[string]map[string]PublishPayload),
	}
}

func (m *Manager) SetWorkerConcurrency(limit int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 1
	}
	m.workerLimit = limit
	m.workerSem = make(chan struct{}, limit)
}

func (m *Manager) SetSessionEventStore(store sessionevent.SessionEventStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionEvents = store
}

func (m *Manager) SetREPLFactory(factory REPLFactory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replFactory = factory
}

func (m *Manager) SetBridgeProvider(provider BridgeProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bridgeFactory = provider
}

func (m *Manager) Store() Store {
	if m == nil {
		return nil
	}
	return m.store
}

func (m *Manager) LoadRuntime(ctx context.Context, sessionID string) (RuntimeState, error) {
	if m == nil || m.store == nil {
		return RuntimeState{}, ErrStoreUnavailable
	}
	return m.store.LoadRuntime(ctx, sessionID)
}

func (m *Manager) LoadOrCreate(ctx context.Context, sessionID string) (*SessionRuntime, error) {
	if m == nil || m.store == nil {
		return nil, ErrStoreUnavailable
	}

	m.mu.Lock()
	if rt, ok := m.runtimes[sessionID]; ok {
		m.mu.Unlock()
		return rt, nil
	}
	m.mu.Unlock()

	state, err := m.store.LoadRuntime(ctx, sessionID)
	if err != nil && !errors.Is(err, ErrRuntimeNotFound) {
		return nil, err
	}

	branchID := "main"
	if errors.Is(err, ErrRuntimeNotFound) {
		branchID, err = m.ensureSessionEventSession(ctx, sessionID)
		if err != nil {
			return nil, err
		}

		state = RuntimeState{
			SessionID:      sessionID,
			ActiveBranchID: branchID,
			Shared:         map[string]any{},
			OutputData:     map[string]any{},
			Artifacts:      map[string]ArtifactRef{},
		}
		if err := m.store.SaveRuntime(ctx, state); err != nil {
			return nil, err
		}
		initialBranch := BranchState{
			BranchID:      branchID,
			Status:        BranchStatusActive,
			BranchLocal:   map[string]any{},
			JournalOffset: 0,
		}
		if err := m.store.SaveBranch(ctx, sessionID, initialBranch); err != nil {
			return nil, err
		}
	} else {
		if state.ActiveBranchID == "" {
			state.ActiveBranchID = branchID
			if err := m.store.SaveRuntime(ctx, state); err != nil {
				return nil, err
			}
		}
		if _, err := m.ensureSessionEventSession(ctx, sessionID); err != nil {
			return nil, err
		}
	}

	branches, err := m.store.ListBranches(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	branchMap := make(map[string]BranchState, len(branches))
	for _, branch := range branches {
		branchMap[branch.BranchID] = branch
	}

	var repl *rlm.YaegiREPL
	m.mu.Lock()
	factory := m.replFactory
	m.mu.Unlock()
	if factory != nil {
		repl, err = factory()
		if err != nil {
			return nil, err
		}
	}

	runtime := &SessionRuntime{
		SessionID: sessionID,
		State:     state,
		Branches:  branchMap,
		REPL:      repl,
	}

	if runtime.REPL != nil {
		if _, err := m.ensureRuntimeBridge(ctx, runtime, ""); err != nil {
			return nil, err
		}
		if err := m.restoreActiveBranch(ctx, runtime); err != nil {
			return nil, err
		}
	}

	if err == nil {
		if offset, appendErr := m.store.AppendJournal(ctx, sessionID, runtime.State.ActiveBranchID, JournalEntry{
			Timestamp: time.Now().UTC(),
			Kind:      "recovery_rehydrated",
			Payload: map[string]any{
				"source": "runtime_store",
			},
		}); appendErr == nil {
			if branch, ok := runtime.Branches[runtime.State.ActiveBranchID]; ok {
				branch.JournalOffset = offset
				runtime.Branches[runtime.State.ActiveBranchID] = branch
			}
		}
	}

	m.mu.Lock()
	m.runtimes[sessionID] = runtime
	m.mu.Unlock()

	return runtime, nil
}

func (m *Manager) RunSerialized(ctx context.Context, sessionID string, fn func(context.Context, *SessionRuntime) error) error {
	runtime, err := m.LoadOrCreate(ctx, sessionID)
	if err != nil {
		return err
	}

	runtime.episodeMu.Lock()
	defer runtime.episodeMu.Unlock()

	episodeCtx, cancel := context.WithCancel(ctx)
	runtime.setCancel(cancel)
	defer func() {
		cancel()
		runtime.clearCancel()
	}()

	return fn(episodeCtx, runtime)
}

func (m *Manager) CancelSession(sessionID string) {
	m.mu.Lock()
	runtime := m.runtimes[sessionID]
	m.mu.Unlock()
	if runtime != nil {
		runtime.cancelEpisode()
	}
}

func (m *Manager) UnloadSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.runtimes, sessionID)
	delete(m.published, sessionID)
}

func (m *Manager) SaveSession(ctx context.Context, runtime *SessionRuntime) error {
	if runtime == nil {
		return fmt.Errorf("runtime is nil")
	}
	if err := m.store.SaveRuntime(ctx, runtime.State); err != nil {
		return err
	}
	for _, branch := range runtime.Branches {
		if err := m.store.SaveBranch(ctx, runtime.SessionID, branch); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) CheckpointActive(ctx context.Context, runtime *SessionRuntime, replay []string, completion NormalizedCompletion) (Checkpoint, error) {
	if runtime == nil {
		return Checkpoint{}, fmt.Errorf("runtime is nil")
	}
	branchID := runtime.State.ActiveBranchID
	branch, ok := runtime.Branches[branchID]
	if !ok {
		return Checkpoint{}, ErrBranchNotFound
	}

	state := map[string]any{}
	if runtime.REPL != nil {
		exported, err := runtime.REPL.ExportState()
		if err != nil {
			return Checkpoint{}, err
		}
		maps.Copy(state, exported)
	}

	cp, err := m.store.WriteCheckpoint(ctx, runtime.SessionID, branch, state, replay, completion)
	if err != nil {
		return Checkpoint{}, err
	}

	branch.HeadCheckpoint = cp.ID
	branch.Status = BranchStatusActive
	runtime.Branches[branchID] = branch
	runtime.State.ActiveBranchID = branchID

	if err := m.store.PromoteBranchHead(ctx, runtime.SessionID, branchID, cp.ID); err != nil {
		return Checkpoint{}, err
	}
	if err := m.SaveSession(ctx, runtime); err != nil {
		return Checkpoint{}, err
	}

	return cp, nil
}

func (m *Manager) ensureRuntimeBridge(ctx context.Context, runtime *SessionRuntime, messageID string) (*HostBridge, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime is nil")
	}

	m.mu.Lock()
	provider := m.bridgeFactory
	m.mu.Unlock()

	var (
		messagesSvc message.Service
		agentTools  []fantasy.AgentTool
		err         error
	)
	if provider != nil {
		messagesSvc, agentTools, err = provider(ctx, runtime.SessionID)
		if err != nil {
			return nil, err
		}
	}

	if runtime.bridge == nil {
		runtime.bridge = NewHostBridge(runtime.SessionID, runtime, m, messagesSvc, agentTools)
	} else {
		runtime.bridge.Configure(messagesSvc, agentTools)
	}
	runtime.bridge.SetContext(ctx, messageID)

	return runtime.bridge, nil
}

func (m *Manager) restoreActiveBranch(ctx context.Context, runtime *SessionRuntime) error {
	if runtime == nil || runtime.REPL == nil {
		return nil
	}
	if err := runtime.REPL.Reset(); err != nil {
		return err
	}
	runtime.bridgeInjected = false
	if runtime.bridge != nil {
		if err := runtime.bridge.InjectSymbols(runtime.REPL); err != nil {
			return err
		}
		runtime.bridgeInjected = true
	}

	branchID := runtime.State.ActiveBranchID
	if branchID == "" {
		return nil
	}
	branch, ok := runtime.Branches[branchID]
	if !ok {
		return ErrBranchNotFound
	}

	if branch.HeadCheckpoint == "" {
		baseline := map[string]any{
			"shared":         maps.Clone(runtime.State.Shared),
			"branch_local":   maps.Clone(branch.BranchLocal),
			"done":           runtime.State.Done,
			"output_message": runtime.State.OutputMessage,
			"output_data":    maps.Clone(runtime.State.OutputData),
			"artifacts":      maps.Clone(runtime.State.Artifacts),
		}
		return runtime.REPL.ImportState(baseline)
	}

	_, state, _, replay, err := m.store.LoadCheckpoint(ctx, runtime.SessionID, branchID, branch.HeadCheckpoint)
	if err != nil {
		return err
	}
	if err := runtime.REPL.ImportState(state); err != nil {
		return err
	}
	runtime.clearDirty()
	for _, block := range replay {
		if strings.TrimSpace(block) == "" {
			continue
		}
		if _, err := runtime.REPL.Execute(ctx, block); err != nil {
			return err
		}
	}
	return syncDirtyRuntimeStateIntoREPL(runtime)
}

func (m *Manager) ensureSessionEventSession(ctx context.Context, sessionID string) (string, error) {
	if m.sessionEvents == nil {
		return "main", nil
	}

	sessionObj, err := m.sessionEvents.GetSession(ctx, sessionID)
	if err == nil {
		if sessionObj.ActiveBranchID != "" {
			return sessionObj.ActiveBranchID, nil
		}
		return "main", nil
	}

	createdSession, defaultBranch, err := m.sessionEvents.CreateSession(ctx, sessionevent.CreateSessionParams{
		ID:         sessionID,
		Title:      "Crush RLM Session",
		BranchName: "main",
		Metadata: map[string]any{
			"created_by": "crush_rlm_runtime",
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
	if err != nil {
		return "", err
	}
	if createdSession != nil && createdSession.ActiveBranchID != "" {
		return createdSession.ActiveBranchID, nil
	}
	if defaultBranch != nil && defaultBranch.ID != "" {
		return defaultBranch.ID, nil
	}
	return uuid.NewString(), nil
}

func (m *Manager) Publish(ctx context.Context, sessionID string, payload PublishPayload) (string, error) {
	runtime, err := m.LoadOrCreate(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if payload.ID == "" {
		payload.ID = uuid.NewString()
	}
	if payload.CreatedAt.IsZero() {
		payload.CreatedAt = time.Now().UTC()
	}

	m.mu.Lock()
	if _, ok := m.published[sessionID]; !ok {
		m.published[sessionID] = map[string]PublishPayload{}
	}
	m.published[sessionID][payload.ID] = payload
	m.mu.Unlock()

	_, _ = m.store.AppendJournal(ctx, sessionID, runtime.State.ActiveBranchID, JournalEntry{
		Timestamp: payload.CreatedAt,
		Kind:      "publish",
		Payload: map[string]any{
			"publish_id": payload.ID,
			"summary":    payload.Summary,
		},
	})

	return payload.ID, nil
}

func (m *Manager) Commit(ctx context.Context, sessionID, publishID string) error {
	runtime, err := m.LoadOrCreate(ctx, sessionID)
	if err != nil {
		return err
	}

	m.mu.Lock()
	payload, ok := m.published[sessionID][publishID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("publish payload not found: %s", publishID)
	}
	delete(m.published[sessionID], publishID)
	m.mu.Unlock()

	if runtime.State.Shared == nil {
		runtime.State.Shared = map[string]any{}
	}
	maps.Copy(runtime.State.Shared, payload.SharedUpdates)
	if runtime.State.Artifacts == nil {
		runtime.State.Artifacts = map[string]ArtifactRef{}
	}
	maps.Copy(runtime.State.Artifacts, payload.Artifacts)

	branch := runtime.Branches[runtime.State.ActiveBranchID]
	if branch.BranchLocal == nil {
		branch.BranchLocal = map[string]any{}
	}
	maps.Copy(branch.BranchLocal, payload.BranchLocalDelta)
	runtime.Branches[runtime.State.ActiveBranchID] = branch

	if err := m.SaveSession(ctx, runtime); err != nil {
		return err
	}

	_, _ = m.store.AppendJournal(ctx, sessionID, runtime.State.ActiveBranchID, JournalEntry{
		Timestamp: time.Now().UTC(),
		Kind:      "commit",
		Payload: map[string]any{
			"publish_id": publishID,
			"summary":    payload.Summary,
		},
	})

	return nil
}

func (m *Manager) ListPublished(sessionID string) []PublishPayload {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.published[sessionID]
	out := make([]PublishPayload, 0, len(items))
	for _, payload := range items {
		out = append(out, payload)
	}
	return out
}
