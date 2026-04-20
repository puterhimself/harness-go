package rlmruntime

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"sync"
	"time"

	"charm.land/fantasy"
	"github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/charmbracelet/crush/internal/agent/tools"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/google/uuid"
)

type HostBridge struct {
	sessionID  string
	runtime    *SessionRuntime
	manager    *Manager
	messages   message.Service
	toolByName map[string]fantasy.AgentTool

	mu        sync.RWMutex
	ctx       context.Context
	messageID string
}

func NewHostBridge(sessionID string, runtime *SessionRuntime, manager *Manager, messages message.Service, agentTools []fantasy.AgentTool) *HostBridge {
	b := &HostBridge{
		sessionID: sessionID,
		runtime:   runtime,
		manager:   manager,
		ctx:       context.Background(),
	}
	b.Configure(messages, agentTools)
	return b
}

func (b *HostBridge) Configure(messages message.Service, agentTools []fantasy.AgentTool) {
	toolByName := make(map[string]fantasy.AgentTool, len(agentTools))
	for _, tool := range agentTools {
		if tool == nil {
			continue
		}
		toolByName[tool.Info().Name] = tool
	}

	b.mu.Lock()
	b.messages = messages
	b.toolByName = toolByName
	b.mu.Unlock()
}

func (b *HostBridge) SetContext(ctx context.Context, messageID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	b.ctx = ctx
	b.messageID = messageID
}

func (b *HostBridge) InjectSymbols(repl *rlm.YaegiREPL) error {
	if repl == nil {
		return nil
	}
	return repl.InjectSymbols(map[string]reflect.Value{
		"ToolCall":         reflect.ValueOf(b.ToolCall),
		"ReadMessages":     reflect.ValueOf(b.ReadMessages),
		"GetShared":        reflect.ValueOf(b.GetShared),
		"SetShared":        reflect.ValueOf(b.SetShared),
		"GetBranchLocal":   reflect.ValueOf(b.GetBranchLocal),
		"SetBranchLocal":   reflect.ValueOf(b.SetBranchLocal),
		"SetDone":          reflect.ValueOf(b.SetDone),
		"SetOutputMessage": reflect.ValueOf(b.SetOutputMessage),
		"SetOutputData":    reflect.ValueOf(b.SetOutputData),
		"PutArtifact":      reflect.ValueOf(b.PutArtifact),
		"ForkBranch":       reflect.ValueOf(b.ForkBranch),
		"Publish":          reflect.ValueOf(b.Publish),
		"Commit":           reflect.ValueOf(b.Commit),
	})
}

func (b *HostBridge) ToolCall(name string, input map[string]any) map[string]any {
	tool := b.toolByName[name]
	if tool == nil {
		return map[string]any{"ok": false, "error": "tool not found"}
	}

	ctx, callID := b.callContext()
	_, _ = b.appendJournal(ctx, "tool_call", map[string]any{"tool": name, "input": input, "call_id": callID})

	inputJSON, _ := json.Marshal(input)
	response, err := tool.Run(ctx, fantasy.ToolCall{ID: callID, Name: name, Input: string(inputJSON)})
	if err != nil {
		_ = b.appendJournalError(ctx, "tool_result", name, err)
		return map[string]any{"ok": false, "error": err.Error(), "permission_denied": errors.Is(err, permission.ErrorPermissionDenied)}
	}

	payload := map[string]any{
		"ok":         !response.IsError,
		"is_error":   response.IsError,
		"content":    response.Content,
		"type":       response.Type,
		"data":       response.Data,
		"media_type": response.MediaType,
		"metadata":   response.Metadata,
		"stop_turn":  response.StopTurn,
	}
	_, _ = b.appendJournal(ctx, "tool_result", map[string]any{"tool": name, "response": payload, "call_id": callID})
	return payload
}

func (b *HostBridge) ReadMessages(limit int, filter string) []map[string]any {
	if b.messages == nil {
		return nil
	}

	ctx, _ := b.callContext()
	msgs, err := b.messages.List(ctx, b.sessionID)
	if err != nil {
		return nil
	}
	if limit <= 0 || limit > len(msgs) {
		limit = len(msgs)
	}

	result := make([]map[string]any, 0, limit)
	for i := len(msgs) - limit; i < len(msgs); i++ {
		msg := msgs[i]
		if filter != "" && string(msg.Role) != filter {
			continue
		}
		result = append(result, map[string]any{
			"id":         msg.ID,
			"role":       string(msg.Role),
			"content":    msg.Content().Text,
			"created_at": msg.CreatedAt,
		})
	}
	return result
}

func (b *HostBridge) GetShared() map[string]any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return maps.Clone(b.runtime.State.Shared)
}

func (b *HostBridge) SetShared(shared map[string]any) {
	b.mu.Lock()
	b.runtime.State.Shared = maps.Clone(shared)
	b.mu.Unlock()
	b.runtime.markDirty("shared")
	_, _ = b.appendJournal(context.Background(), "state_update", map[string]any{"key": "shared"})
}

func (b *HostBridge) GetBranchLocal() map[string]any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	branch := b.runtime.Branches[b.runtime.State.ActiveBranchID]
	return maps.Clone(branch.BranchLocal)
}

func (b *HostBridge) SetBranchLocal(state map[string]any) {
	b.mu.Lock()
	branch := b.runtime.Branches[b.runtime.State.ActiveBranchID]
	branch.BranchLocal = maps.Clone(state)
	b.runtime.Branches[b.runtime.State.ActiveBranchID] = branch
	b.mu.Unlock()
	b.runtime.markDirty("branch_local")
	_, _ = b.appendJournal(context.Background(), "state_update", map[string]any{"key": "branch_local"})
}

func (b *HostBridge) SetDone(done bool) {
	b.mu.Lock()
	b.runtime.State.Done = done
	b.mu.Unlock()
	b.runtime.markDirty("done")
}

func (b *HostBridge) SetOutputMessage(message string) {
	b.mu.Lock()
	b.runtime.State.OutputMessage = message
	b.mu.Unlock()
	b.runtime.markDirty("output_message")
}

func (b *HostBridge) SetOutputData(data map[string]any) {
	b.mu.Lock()
	b.runtime.State.OutputData = maps.Clone(data)
	b.mu.Unlock()
	b.runtime.markDirty("output_data")
}

func (b *HostBridge) PutArtifact(name string, value any) string {
	ctx, _ := b.callContext()
	branchID := b.runtime.State.ActiveBranchID
	artifact, err := b.manager.store.PutArtifact(ctx, b.sessionID, branchID, name, value)
	if err != nil {
		return ""
	}

	b.mu.Lock()
	if b.runtime.State.Artifacts == nil {
		b.runtime.State.Artifacts = make(map[string]ArtifactRef)
	}
	b.runtime.State.Artifacts[name] = artifact
	b.mu.Unlock()
	b.runtime.markDirty("artifacts")
	_, _ = b.appendJournal(ctx, "artifact_put", map[string]any{"name": name, "artifact_id": artifact.ID})
	return artifact.ID
}

func (b *HostBridge) ForkBranch(name string) string {
	ctx, _ := b.callContext()
	branch, err := b.manager.ForkBranch(ctx, b.sessionID, name)
	if err != nil {
		return ""
	}
	return branch.BranchID
}

func (b *HostBridge) Publish(payload map[string]any) string {
	ctx, _ := b.callContext()
	publishID, err := b.manager.Publish(ctx, b.sessionID, PublishPayload{
		Summary:          asString(payload["summary"]),
		OutputData:       asMap(payload["output_data"]),
		SharedUpdates:    asMap(payload["shared_updates"]),
		BranchLocalDelta: asMap(payload["branch_local_delta"]),
	})
	if err != nil {
		return ""
	}
	return publishID
}

func (b *HostBridge) Commit(payload map[string]any) bool {
	ctx, _ := b.callContext()
	publishID := asString(payload["publish_id"])
	if publishID == "" {
		return false
	}
	return b.manager.Commit(ctx, b.sessionID, publishID) == nil
}

func (b *HostBridge) callContext() (context.Context, string) {
	b.mu.RLock()
	ctx := b.ctx
	messageID := b.messageID
	b.mu.RUnlock()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, tools.SessionIDContextKey, b.sessionID)
	ctx = context.WithValue(ctx, tools.MessageIDContextKey, messageID)
	return ctx, uuid.NewString()
}

func (b *HostBridge) appendJournal(ctx context.Context, kind string, payload map[string]any) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	branchID := b.runtime.State.ActiveBranchID
	offset, err := b.manager.store.AppendJournal(ctx, b.sessionID, branchID, JournalEntry{
		Timestamp: time.Now().UTC(),
		Kind:      kind,
		Payload:   payload,
	})
	if err != nil {
		return 0, err
	}
	branch := b.runtime.Branches[branchID]
	branch.JournalOffset = offset
	b.runtime.Branches[branchID] = branch
	return offset, nil
}

func (b *HostBridge) appendJournalError(ctx context.Context, kind, tool string, err error) error {
	_, appendErr := b.appendJournal(ctx, kind, map[string]any{
		"tool":  tool,
		"error": err.Error(),
	})
	return appendErr
}
