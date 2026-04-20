package rlmruntime

import (
	"context"
	"errors"

	"charm.land/fantasy"
	"github.com/XiaoConstantine/dspy-go/pkg/core"
)

var errUnsupportedLLMOperation = errors.New("operation is not supported by fantasy adapter")

type FantasyLLMAdapter struct {
	model fantasy.LanguageModel
}

func NewFantasyLLMAdapter(model fantasy.LanguageModel) *FantasyLLMAdapter {
	return &FantasyLLMAdapter{model: model}
}

func (a *FantasyLLMAdapter) Generate(ctx context.Context, prompt string, opts ...core.GenerateOption) (*core.LLMResponse, error) {
	if a == nil || a.model == nil {
		return nil, errors.New("missing fantasy model")
	}

	genOpts := core.NewGenerateOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(genOpts)
		}
	}

	call := fantasy.Call{
		Prompt: fantasy.Prompt{fantasy.NewUserMessage(prompt)},
	}
	if genOpts.MaxTokens > 0 {
		max := int64(genOpts.MaxTokens)
		call.MaxOutputTokens = &max
	}
	if genOpts.Temperature > 0 {
		temp := genOpts.Temperature
		call.Temperature = &temp
	}
	if genOpts.TopP > 0 {
		topP := genOpts.TopP
		call.TopP = &topP
	}
	if genOpts.PresencePenalty != 0 {
		penalty := genOpts.PresencePenalty
		call.PresencePenalty = &penalty
	}
	if genOpts.FrequencyPenalty != 0 {
		penalty := genOpts.FrequencyPenalty
		call.FrequencyPenalty = &penalty
	}

	response, err := a.model.Generate(ctx, call)
	if err != nil {
		return nil, err
	}

	usage := &core.TokenInfo{}
	content := ""
	if response != nil {
		content = response.Content.Text()
		usage.PromptTokens = int(response.Usage.InputTokens)
		usage.CompletionTokens = int(response.Usage.OutputTokens)
		usage.TotalTokens = int(response.Usage.TotalTokens)
	}

	metadata := map[string]any{}
	if response != nil {
		metadata["finish_reason"] = string(response.FinishReason)
	}

	return &core.LLMResponse{
		Content:  content,
		Usage:    usage,
		Metadata: metadata,
	}, nil
}

func (a *FantasyLLMAdapter) GenerateWithJSON(ctx context.Context, prompt string, opts ...core.GenerateOption) (map[string]interface{}, error) {
	return nil, errUnsupportedLLMOperation
}

func (a *FantasyLLMAdapter) GenerateWithFunctions(ctx context.Context, prompt string, functions []map[string]interface{}, opts ...core.GenerateOption) (map[string]interface{}, error) {
	return nil, errUnsupportedLLMOperation
}

func (a *FantasyLLMAdapter) GenerateWithContent(ctx context.Context, content []core.ContentBlock, opts ...core.GenerateOption) (*core.LLMResponse, error) {
	prompt := ""
	for _, block := range content {
		if block.Text != "" {
			if prompt != "" {
				prompt += "\n"
			}
			prompt += block.Text
		}
	}
	return a.Generate(ctx, prompt, opts...)
}

func (a *FantasyLLMAdapter) StreamGenerate(ctx context.Context, prompt string, opts ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, errUnsupportedLLMOperation
}

func (a *FantasyLLMAdapter) StreamGenerateWithContent(ctx context.Context, content []core.ContentBlock, opts ...core.GenerateOption) (*core.StreamResponse, error) {
	return nil, errUnsupportedLLMOperation
}

func (a *FantasyLLMAdapter) CreateEmbedding(ctx context.Context, input string, opts ...core.EmbeddingOption) (*core.EmbeddingResult, error) {
	return nil, errUnsupportedLLMOperation
}

func (a *FantasyLLMAdapter) CreateEmbeddings(ctx context.Context, inputs []string, opts ...core.EmbeddingOption) (*core.BatchEmbeddingResult, error) {
	return nil, errUnsupportedLLMOperation
}

func (a *FantasyLLMAdapter) ProviderName() string {
	if a == nil || a.model == nil {
		return "fantasy"
	}
	return a.model.Provider()
}

func (a *FantasyLLMAdapter) ModelID() string {
	if a == nil || a.model == nil {
		return "unknown"
	}
	return a.model.Model()
}

func (a *FantasyLLMAdapter) Capabilities() []core.Capability {
	return []core.Capability{core.CapabilityCompletion}
}
