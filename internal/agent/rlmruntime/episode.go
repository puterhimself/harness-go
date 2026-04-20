package rlmruntime

import (
	"context"
	"maps"
	"time"

	"charm.land/fantasy"
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/google/uuid"
)

type EpisodeModule interface {
	RunEpisode(ctx context.Context, replEnv *rlm.YaegiREPL, contextPayload any, query string, opts rlm.EpisodeOptions) (*rlm.CompletionResult, *rlm.RLMTrace, error)
}

type EpisodeRunner struct {
	manager  *Manager
	module   EpisodeModule
	messages message.Service
}

type EpisodeRunParams struct {
	SessionID          string
	UserMessageID      string
	AssistantMessageID string
	Prompt             string
	ContextPayload     any
	Tools              []fantasy.AgentTool
}

type EpisodeRunResult struct {
	Completion NormalizedCompletion
	Trace      *rlm.RLMTrace
	Checkpoint Checkpoint
}

func NewEpisodeRunner(manager *Manager, module EpisodeModule, messages message.Service) *EpisodeRunner {
	return &EpisodeRunner{manager: manager, module: module, messages: messages}
}

func (r *EpisodeRunner) Run(ctx context.Context, params EpisodeRunParams) (EpisodeRunResult, error) {
	var result EpisodeRunResult

	err := r.manager.RunSerialized(ctx, params.SessionID, func(episodeCtx context.Context, runtime *SessionRuntime) error {
		if runtime.REPL == nil {
			return ErrStoreUnavailable
		}

		episodeID := uuid.NewString()
		start := time.Now().UTC()
		runtime.State.Episode = EpisodeState{
			EpisodeID:     episodeID,
			UserMessageID: params.UserMessageID,
			StartedAt:     start,
		}

		runtime.State.Messages = append(runtime.State.Messages, RuntimeMessage{
			ID:        params.UserMessageID,
			Role:      "user",
			Content:   params.Prompt,
			Timestamp: start,
		})

		bridge, err := r.manager.ensureRuntimeBridge(episodeCtx, runtime, params.AssistantMessageID)
		if err != nil {
			return err
		}
		bridge.Configure(r.messages, params.Tools)
		bridge.SetContext(episodeCtx, params.AssistantMessageID)
		if !runtime.bridgeInjected {
			if err := bridge.InjectSymbols(runtime.REPL); err != nil {
				return err
			}
			runtime.bridgeInjected = true
		}

		importState := map[string]any{
			"messages":       runtimeMessagesToMaps(runtime.State.Messages),
			"task":           params.Prompt,
			"shared":         maps.Clone(runtime.State.Shared),
			"branch_local":   maps.Clone(runtime.Branches[runtime.State.ActiveBranchID].BranchLocal),
			"done":           runtime.State.Done,
			"output_message": runtime.State.OutputMessage,
			"output_data":    maps.Clone(runtime.State.OutputData),
			"episode": map[string]any{
				"id":              episodeID,
				"user_message_id": params.UserMessageID,
				"started_at":      start.Format(time.RFC3339Nano),
			},
		}
		if err := runtime.REPL.ImportState(importState); err != nil {
			return err
		}

		contextPayload := params.ContextPayload
		if contextPayload == nil {
			contextPayload = map[string]any{"task": params.Prompt}
		}
		runtime.clearDirty()

		completionResult, trace, err := r.module.RunEpisode(episodeCtx, runtime.REPL, contextPayload, params.Prompt, rlm.EpisodeOptions{
			LoadContext:    false,
			ApplyREPLSetup: false,
		})
		if err != nil {
			return err
		}

		exported, err := runtime.REPL.ExportState()
		if err != nil {
			return err
		}
		mergeDirtyRuntimeState(exported, runtime)

		normalized := normalizeCompletion(completionResult, trace, runtime.REPL, exported)
		runtime.State.Done = normalized.Done
		runtime.State.OutputMessage = normalized.OutputMessage
		runtime.State.OutputData = maps.Clone(normalized.OutputData)
		if runtime.State.OutputData == nil {
			runtime.State.OutputData = map[string]any{}
		}
		if runtime.State.Artifacts == nil {
			runtime.State.Artifacts = map[string]ArtifactRef{}
		}
		maps.Copy(runtime.State.Artifacts, normalized.Artifacts)

		if shared := asMap(exported["shared"]); shared != nil {
			runtime.State.Shared = shared
		}
		if branchLocal := asMap(exported["branch_local"]); branchLocal != nil {
			branch := runtime.Branches[runtime.State.ActiveBranchID]
			branch.BranchLocal = branchLocal
			runtime.Branches[runtime.State.ActiveBranchID] = branch
		}

		finishedAt := time.Now().UTC()
		runtime.State.Episode.FinishedAt = finishedAt
		if trace != nil {
			runtime.State.Episode.TerminationCause = trace.TerminationCause
		}

		if normalized.OutputMessage != "" {
			runtime.State.Messages = append(runtime.State.Messages, RuntimeMessage{
				ID:        params.AssistantMessageID,
				Role:      "assistant",
				Content:   normalized.OutputMessage,
				Timestamp: finishedAt,
			})
		}

		checkpoint, err := r.manager.CheckpointActive(episodeCtx, runtime, replayFromTrace(trace), normalized, traceFromRLM(trace))
		if err != nil {
			return err
		}
		if err := r.manager.SaveSession(episodeCtx, runtime); err != nil {
			return err
		}

		result = EpisodeRunResult{
			Completion: normalized,
			Trace:      trace,
			Checkpoint: checkpoint,
		}
		return nil
	})
	if err != nil {
		return EpisodeRunResult{}, err
	}

	return result, nil
}

func normalizeCompletion(result *rlm.CompletionResult, trace *rlm.RLMTrace, repl *rlm.YaegiREPL, exported map[string]any) NormalizedCompletion {
	completion := NormalizedCompletion{
		OutputData: map[string]any{},
		Artifacts:  map[string]ArtifactRef{},
	}
	explicit := false

	if repl != nil && repl.HasSubmit() {
		submit := repl.GetSubmitOutput()
		if outputMessage := asString(submit["output_message"]); outputMessage != "" {
			completion.Done = true
			completion.OutputMessage = outputMessage
			explicit = true
		}
		if explicit {
			if outputData := asMap(submit["output_data"]); outputData != nil {
				completion.OutputData = outputData
			}
		}
	}

	if !explicit && repl != nil && repl.HasFinal() {
		if final := repl.Final(); final != "" {
			completion.Done = true
			completion.OutputMessage = final
			explicit = true
		}
	}

	done, _ := exported["done"].(bool)
	outputMessage := asString(exported["output_message"])
	if !explicit && done && outputMessage != "" {
		completion.Done = true
		completion.OutputMessage = outputMessage
		explicit = true
		if outputData := asMap(exported["output_data"]); outputData != nil {
			completion.OutputData = outputData
		}
	}

	if explicit {
		if outputData := asMap(exported["output_data"]); outputData != nil && len(completion.OutputData) == 0 {
			completion.OutputData = outputData
		}
	}

	if summary := asString(exported["branch_summary"]); summary != "" {
		completion.BranchSummary = summary
	}

	if completion.OutputData == nil {
		completion.OutputData = map[string]any{}
	}

	if completion.OutputMessage == "" && explicit && result != nil {
		completion.OutputMessage = result.Response
	}
	if trace != nil {
		completion.Reason = trace.TerminationCause
	}

	return completion
}

func replayFromTrace(trace *rlm.RLMTrace) []string {
	if trace == nil {
		return nil
	}
	lines := []string{}
	for _, step := range trace.Steps {
		if step.Code != "" {
			lines = append(lines, step.Code)
		}
	}
	return lines
}

func mergeDirtyRuntimeState(target map[string]any, runtime *SessionRuntime) {
	if target == nil || runtime == nil {
		return
	}
	dirty := runtime.dirtySet()
	if len(dirty) == 0 {
		return
	}
	if _, ok := dirty["shared"]; ok {
		target["shared"] = maps.Clone(runtime.State.Shared)
	}
	if _, ok := dirty["branch_local"]; ok {
		branchLocal := map[string]any{}
		if branch, exists := runtime.Branches[runtime.State.ActiveBranchID]; exists {
			branchLocal = maps.Clone(branch.BranchLocal)
		}
		target["branch_local"] = branchLocal
	}
	if _, ok := dirty["done"]; ok {
		target["done"] = runtime.State.Done
	}
	if _, ok := dirty["output_message"]; ok {
		target["output_message"] = runtime.State.OutputMessage
	}
	if _, ok := dirty["output_data"]; ok {
		target["output_data"] = maps.Clone(runtime.State.OutputData)
	}
	if _, ok := dirty["artifacts"]; ok {
		target["artifacts"] = maps.Clone(runtime.State.Artifacts)
	}
}

func syncDirtyRuntimeStateIntoREPL(runtime *SessionRuntime) error {
	if runtime == nil || runtime.REPL == nil {
		return nil
	}
	state := map[string]any{}
	mergeDirtyRuntimeState(state, runtime)
	if len(state) == 0 {
		return nil
	}
	return runtime.REPL.ImportState(state)
}

func runtimeMessagesToMaps(messages []RuntimeMessage) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		out = append(out, map[string]any{
			"id":        msg.ID,
			"role":      msg.Role,
			"content":   msg.Content,
			"timestamp": msg.Timestamp.Format(time.RFC3339Nano),
			"metadata":  maps.Clone(msg.Metadata),
		})
	}
	return out
}

func asMap(value any) map[string]any {
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return maps.Clone(m)
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
