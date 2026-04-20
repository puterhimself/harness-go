package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"charm.land/fantasy"
	se "github.com/XiaoConstantine/dspy-go/pkg/agents/sessionevent"
	dspyrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"
	"github.com/charmbracelet/crush/internal/agent/notify"
	"github.com/charmbracelet/crush/internal/agent/rlmruntime"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/charmbracelet/crush/internal/session"
)

type rlmCoordinator struct {
	base          *coordinator
	cfg           *config.ConfigStore
	sessions      session.Service
	messages      message.Service
	runtimeStore  *rlmruntime.FileStore
	runtimeMgr    *rlmruntime.Manager
	sessionEvents *se.SQLiteStore

	mu     sync.Mutex
	active map[string]context.CancelFunc
	queued map[string][]string
}

func NewRLMCoordinator(
	ctx context.Context,
	cfg *config.ConfigStore,
	sessions session.Service,
	messages message.Service,
	permissions permission.Service,
	history history.Service,
	filetracker filetracker.Service,
	lspManager *lsp.Manager,
	notify pubsub.Publisher[notify.Notification],
) (Coordinator, error) {
	fallback, err := NewCoordinator(
		ctx,
		cfg,
		sessions,
		messages,
		permissions,
		history,
		filetracker,
		lspManager,
		notify,
	)
	if err != nil {
		return nil, err
	}

	base, ok := fallback.(*coordinator)
	if !ok {
		return nil, fmt.Errorf("unexpected coordinator type %T", fallback)
	}

	runtimeRoot := filepath.Join(cfg.WorkingDir(), cfg.Config().Options.DataDirectory, "rlm")
	runtimeStore := rlmruntime.NewFileStore(runtimeRoot)

	sessionEventStore, err := se.NewSQLiteStore(filepath.Join(runtimeRoot, "sessionevent.sqlite"))
	if err != nil {
		return nil, err
	}

	runtimeMgr := rlmruntime.NewManager(runtimeStore)
	runtimeMgr.SetSessionEventStore(sessionEventStore)
	runtimeMgr.SetBridgeProvider(func(ctx context.Context, sessionID string) (message.Service, []fantasy.AgentTool, error) {
		agentCfg, ok := cfg.Config().Agents[config.AgentCoder]
		if !ok {
			return messages, nil, errCoderAgentNotConfigured
		}
		tools, err := base.buildTools(ctx, agentCfg)
		if err != nil {
			return messages, nil, err
		}
		return messages, tools, nil
	})

	return &rlmCoordinator{
		base:          base,
		cfg:           cfg,
		sessions:      sessions,
		messages:      messages,
		runtimeStore:  runtimeStore,
		runtimeMgr:    runtimeMgr,
		sessionEvents: sessionEventStore,
		active:        make(map[string]context.CancelFunc),
		queued:        make(map[string][]string),
	}, nil
}

func (c *rlmCoordinator) Run(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) (*fantasy.AgentResult, error) {
	if prompt == "" && !message.ContainsTextAttachment(attachments) {
		return nil, ErrEmptyPrompt
	}
	if sessionID == "" {
		return nil, ErrSessionMissing
	}

	if err := c.base.readyWg.Wait(); err != nil {
		return nil, err
	}
	if err := c.base.UpdateModels(ctx); err != nil {
		return nil, fmt.Errorf("failed to update models: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	if busy := c.markActive(sessionID, prompt, cancel); busy {
		cancel()
		return nil, nil
	}
	defer c.clearActive(sessionID)

	model := c.base.currentAgent.Model()
	agentCfg, ok := c.cfg.Config().Agents[config.AgentCoder]
	if !ok {
		return nil, errCoderAgentNotConfigured
	}
	tools, err := c.base.buildTools(ctx, agentCfg)
	if err != nil {
		return nil, err
	}

	userMsg, err := c.createUserMessage(ctx, sessionID, prompt, attachments)
	if err != nil {
		return nil, err
	}

	assistantMsg, err := c.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:     message.Assistant,
		Parts:    []message.ContentPart{},
		Model:    model.ModelCfg.Model,
		Provider: model.ModelCfg.Provider,
	})
	if err != nil {
		return nil, err
	}

	adapter := rlmruntime.NewFantasyLLMAdapter(model.Model)
	c.runtimeMgr.SetREPLFactory(func() (*dspyrlm.YaegiREPL, error) {
		return dspyrlm.NewYaegiREPL(dspyrlm.NewLLMSubClient(adapter))
	})

	rlmModule := dspyrlm.NewFromLLM(adapter,
		dspyrlm.WithMaxIterations(30),
		dspyrlm.WithTimeout(2*time.Minute),
	)
	if model.ModelCfg.MaxTokens > 0 {
		rlmModule.WithOptions(dspyrlm.WithMaxTokens(int(model.ModelCfg.MaxTokens)))
	}

	runner := rlmruntime.NewEpisodeRunner(c.runtimeMgr, rlmModule, c.messages)
	runResult, err := runner.Run(ctx, rlmruntime.EpisodeRunParams{
		SessionID:          sessionID,
		UserMessageID:      userMsg.ID,
		AssistantMessageID: assistantMsg.ID,
		Prompt:             message.PromptWithTextAttachments(prompt, attachments),
		Tools:              tools,
	})
	if err != nil {
		assistantMsg.AppendContent("error running RLM runtime")
		assistantMsg.AddFinish(message.FinishReasonError, "RLM runtime error", err.Error())
		_ = c.messages.Update(context.Background(), assistantMsg)
		return nil, err
	}

	assistantMsg.AppendContent(runResult.Completion.OutputMessage)
	assistantMsg.AddFinish(message.FinishReasonEndTurn, "", "")
	if err := c.messages.Update(ctx, assistantMsg); err != nil {
		return nil, err
	}

	return &fantasy.AgentResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: runResult.Completion.OutputMessage},
			},
		},
	}, nil
}

func (c *rlmCoordinator) createUserMessage(ctx context.Context, sessionID, prompt string, attachments []message.Attachment) (message.Message, error) {
	parts := []message.ContentPart{message.TextContent{Text: prompt}}
	for _, attachment := range attachments {
		parts = append(parts, message.BinaryContent{Path: attachment.FilePath, MIMEType: attachment.MimeType, Data: attachment.Content})
	}
	msg, err := c.messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.User,
		Parts: parts,
	})
	if err != nil {
		return message.Message{}, fmt.Errorf("failed to create user message: %w", err)
	}
	return msg, nil
}

func (c *rlmCoordinator) markActive(sessionID, prompt string, cancel context.CancelFunc) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, busy := c.active[sessionID]; busy {
		c.queued[sessionID] = append(c.queued[sessionID], prompt)
		return true
	}
	c.active[sessionID] = cancel
	return false
}

func (c *rlmCoordinator) clearActive(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.active, sessionID)
	delete(c.queued, sessionID)
}

func (c *rlmCoordinator) Cancel(sessionID string) {
	c.mu.Lock()
	cancel := c.active[sessionID]
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.runtimeMgr.CancelSession(sessionID)
}

func (c *rlmCoordinator) CancelAll() {
	c.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(c.active))
	for _, cancel := range c.active {
		cancels = append(cancels, cancel)
	}
	c.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

func (c *rlmCoordinator) IsSessionBusy(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.active[sessionID]
	return ok
}

func (c *rlmCoordinator) IsBusy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active) > 0
}

func (c *rlmCoordinator) QueuedPrompts(sessionID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queued[sessionID])
}

func (c *rlmCoordinator) QueuedPromptsList(sessionID string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	queued := c.queued[sessionID]
	result := make([]string, len(queued))
	copy(result, queued)
	return result
}

func (c *rlmCoordinator) ClearQueue(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.queued, sessionID)
}

func (c *rlmCoordinator) Summarize(ctx context.Context, sessionID string) error {
	return c.base.Summarize(ctx, sessionID)
}

func (c *rlmCoordinator) Model() Model {
	return c.base.Model()
}

func (c *rlmCoordinator) UpdateModels(ctx context.Context) error {
	return c.base.UpdateModels(ctx)
}

func (c *rlmCoordinator) InspectRuntime(ctx context.Context, sessionID string) (rlmruntime.RuntimeInspection, error) {
	if c.runtimeMgr == nil {
		return rlmruntime.RuntimeInspection{}, fmt.Errorf("runtime manager unavailable")
	}
	return c.runtimeMgr.InspectRuntime(ctx, sessionID)
}

func (c *rlmCoordinator) InspectBranchJournal(ctx context.Context, sessionID, branchID string, limit int) ([]rlmruntime.JournalEntry, error) {
	if c.runtimeMgr == nil {
		return nil, fmt.Errorf("runtime manager unavailable")
	}
	return c.runtimeMgr.InspectBranchJournal(ctx, sessionID, branchID, limit)
}

func (c *rlmCoordinator) ForkBranch(ctx context.Context, sessionID, name string) (rlmruntime.BranchState, error) {
	if c.runtimeMgr == nil {
		return rlmruntime.BranchState{}, fmt.Errorf("runtime manager unavailable")
	}
	return c.runtimeMgr.ForkBranch(ctx, sessionID, name)
}

func (c *rlmCoordinator) SwitchBranch(ctx context.Context, sessionID, branchID string) error {
	if c.runtimeMgr == nil {
		return fmt.Errorf("runtime manager unavailable")
	}
	return c.runtimeMgr.SwitchBranch(ctx, sessionID, branchID)
}

func (c *rlmCoordinator) ResumeBranch(ctx context.Context, sessionID, branchID string) error {
	if c.runtimeMgr == nil {
		return fmt.Errorf("runtime manager unavailable")
	}
	return c.runtimeMgr.ResumeBranch(ctx, sessionID, branchID)
}

func (c *rlmCoordinator) WorkerLimit() int {
	if c.runtimeMgr == nil {
		return 0
	}
	return c.runtimeMgr.WorkerLimit()
}

func (c *rlmCoordinator) WorkerUsage() int {
	if c.runtimeMgr == nil {
		return 0
	}
	return c.runtimeMgr.WorkerUsage()
}

func (c *rlmCoordinator) Close() error {
	if c.sessionEvents == nil {
		return nil
	}
	return c.sessionEvents.Close()
}

func (c *rlmCoordinator) runQueuedPrompts(ctx context.Context, sessionID string) error {
	queued := c.QueuedPromptsList(sessionID)
	for _, prompt := range queued {
		if _, err := c.Run(ctx, sessionID, prompt); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}
	return nil
}
