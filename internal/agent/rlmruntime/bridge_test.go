package rlmruntime

import (
	"context"
	"errors"
	"os"
	"testing"

	"charm.land/fantasy"
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/stretchr/testify/require"
)

func TestHostBridgeToolCallsAndState(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetSessionEventStore(newFakeSessionEventStore())
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	runtime, err := manager.LoadOrCreate(context.Background(), "session-1")
	require.NoError(t, err)

	okTool := &fakeTool{name: "ok_tool", run: func(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
		return fantasy.NewTextResponse("ok"), nil
	}}
	deniedTool := &fakeTool{name: "denied_tool", run: func(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
		return fantasy.ToolResponse{}, permission.ErrorPermissionDenied
	}}

	bridge := NewHostBridge("session-1", runtime, manager, nil, []fantasy.AgentTool{okTool, deniedTool})
	bridge.SetContext(context.Background(), "assistant-1")

	res := bridge.ToolCall("ok_tool", map[string]any{"value": "x"})
	require.Equal(t, true, res["ok"])
	require.Equal(t, "ok", res["content"])

	res = bridge.ToolCall("denied_tool", map[string]any{})
	require.Equal(t, false, res["ok"])
	require.Equal(t, true, res["permission_denied"])

	bridge.SetShared(map[string]any{"a": "b"})
	require.Equal(t, "b", bridge.GetShared()["a"])

	bridge.SetBranchLocal(map[string]any{"local": true})
	require.Equal(t, true, bridge.GetBranchLocal()["local"])

	artifactID := bridge.PutArtifact("result", map[string]any{"ok": true})
	require.NotEmpty(t, artifactID)
	require.NotEmpty(t, runtime.State.Artifacts["result"].Path)

	_, err = os.Stat(runtime.State.Artifacts["result"].Path)
	require.NoError(t, err)
}

type fakeTool struct {
	name string
	run  func(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error)
}

func (t *fakeTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: t.name, Description: t.name}
}

func (t *fakeTool) Run(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if t.run == nil {
		return fantasy.NewTextErrorResponse("missing run"), nil
	}
	return t.run(ctx, params)
}

func (t *fakeTool) ProviderOptions() fantasy.ProviderOptions { return nil }

func (t *fakeTool) SetProviderOptions(opts fantasy.ProviderOptions) {}

func TestHostBridgePermissionDenialErrorPath(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })
	runtime, err := manager.LoadOrCreate(context.Background(), "session-1")
	require.NoError(t, err)

	tool := &fakeTool{name: "failing", run: func(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
		return fantasy.ToolResponse{}, errors.New("boom")
	}}

	bridge := NewHostBridge("session-1", runtime, manager, nil, []fantasy.AgentTool{tool})
	res := bridge.ToolCall("failing", map[string]any{})
	require.Equal(t, false, res["ok"])
	require.Equal(t, false, res["permission_denied"])
}
