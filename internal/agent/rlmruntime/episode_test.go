package rlmruntime

import (
	"context"
	"testing"

	"charm.land/fantasy"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestEpisodeRunnerRunPersistsCompletionAndCheckpoint(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetSessionEventStore(newFakeSessionEventStore())
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	module := &fakeEpisodeModule{run: func(ctx context.Context, replEnv *rlm.YaegiREPL, contextPayload any, query string, opts rlm.EpisodeOptions) (*rlm.CompletionResult, *rlm.RLMTrace, error) {
		require.NoError(t, replEnv.SetValue("done", true))
		require.NoError(t, replEnv.SetValue("output_message", "final output"))
		require.NoError(t, replEnv.SetValue("output_data", map[string]any{"status": "ok"}))
		trace := &rlm.RLMTrace{
			TerminationCause: "final_answer",
			Steps:            []rlm.RLMTraceStep{{Index: 1, Code: "x := 1"}},
			Usage:            core.TokenUsage{TotalTokens: 10},
		}
		return &rlm.CompletionResult{Response: "ignored"}, trace, nil
	}}

	runner := NewEpisodeRunner(manager, module, nil)
	result, err := runner.Run(context.Background(), EpisodeRunParams{
		SessionID:          "session-1",
		UserMessageID:      "user-1",
		AssistantMessageID: "assistant-1",
		Prompt:             "solve task",
	})
	require.NoError(t, err)
	require.Equal(t, true, result.Completion.Done)
	require.Equal(t, "final output", result.Completion.OutputMessage)
	require.Equal(t, "ok", result.Completion.OutputData["status"])
	require.NotEmpty(t, result.Checkpoint.ID)

	_, _, _, replay, err := store.LoadCheckpoint(context.Background(), "session-1", result.Checkpoint.BranchID, result.Checkpoint.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"x := 1"}, replay)
}

func TestEpisodeRunnerDoesNotNormalizeFallbackCompletion(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	module := &fakeEpisodeModule{run: func(ctx context.Context, replEnv *rlm.YaegiREPL, contextPayload any, query string, opts rlm.EpisodeOptions) (*rlm.CompletionResult, *rlm.RLMTrace, error) {
		trace := &rlm.RLMTrace{TerminationCause: "max_iterations"}
		return &rlm.CompletionResult{Response: "forced output"}, trace, nil
	}}

	runner := NewEpisodeRunner(manager, module, nil)
	result, err := runner.Run(context.Background(), EpisodeRunParams{
		SessionID:          "session-1",
		UserMessageID:      "user-1",
		AssistantMessageID: "assistant-1",
		Prompt:             "task",
	})
	require.NoError(t, err)
	require.Equal(t, false, result.Completion.Done)
	require.Empty(t, result.Completion.OutputMessage)
	require.Equal(t, "max_iterations", result.Completion.Reason)
}

func TestEpisodeRunnerRefreshesBridgeContextBetweenEpisodes(t *testing.T) {
	t.Parallel()

	store := NewFileStore(t.TempDir())
	manager := NewManager(store)
	manager.SetREPLFactory(func() (*rlm.YaegiREPL, error) { return rlm.NewYaegiREPL(nil) })

	var messageIDs []string
	tool := &fakeTool{name: "ctx_tool", run: func(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
		messageID, _ := ctx.Value(tools.MessageIDContextKey).(string)
		messageIDs = append(messageIDs, messageID)
		return fantasy.NewTextResponse("ok"), nil
	}}

	module := &fakeEpisodeModule{run: func(ctx context.Context, replEnv *rlm.YaegiREPL, contextPayload any, query string, opts rlm.EpisodeOptions) (*rlm.CompletionResult, *rlm.RLMTrace, error) {
		_, err := replEnv.Execute(ctx, `
ToolCall("ctx_tool", map[string]any{})
SetDone(true)
SetOutputMessage("done")
`)
		require.NoError(t, err)
		trace := &rlm.RLMTrace{TerminationCause: "state_done", Steps: []rlm.RLMTraceStep{{Index: 1, Code: "SetDone(true)\nSetOutputMessage(\"done\")"}}}
		return &rlm.CompletionResult{}, trace, nil
	}}

	runner := NewEpisodeRunner(manager, module, nil)
	_, err := runner.Run(context.Background(), EpisodeRunParams{
		SessionID:          "session-1",
		UserMessageID:      "user-1",
		AssistantMessageID: "assistant-1",
		Prompt:             "task 1",
		Tools:              []fantasy.AgentTool{tool},
	})
	require.NoError(t, err)

	_, err = runner.Run(context.Background(), EpisodeRunParams{
		SessionID:          "session-1",
		UserMessageID:      "user-2",
		AssistantMessageID: "assistant-2",
		Prompt:             "task 2",
		Tools:              []fantasy.AgentTool{tool},
	})
	require.NoError(t, err)

	require.Equal(t, []string{"assistant-1", "assistant-2"}, messageIDs)
}

type fakeEpisodeModule struct {
	run func(ctx context.Context, replEnv *rlm.YaegiREPL, contextPayload any, query string, opts rlm.EpisodeOptions) (*rlm.CompletionResult, *rlm.RLMTrace, error)
}

func (f *fakeEpisodeModule) RunEpisode(ctx context.Context, replEnv *rlm.YaegiREPL, contextPayload any, query string, opts rlm.EpisodeOptions) (*rlm.CompletionResult, *rlm.RLMTrace, error) {
	return f.run(ctx, replEnv, contextPayload, query, opts)
}
