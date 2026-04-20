package rlmruntime

import dspyrlm "github.com/XiaoConstantine/dspy-go/pkg/modules/rlm"

func traceFromRLM(trace *dspyrlm.RLMTrace) EpisodeTrace {
	if trace == nil {
		return EpisodeTrace{}
	}

	steps := make([]EpisodeTraceStep, 0, len(trace.Steps))
	for _, step := range trace.Steps {
		steps = append(steps, EpisodeTraceStep{
			Index:       step.Index,
			Thought:     step.Thought,
			Action:      step.Action,
			Code:        step.Code,
			SubQuery:    step.SubQuery,
			Observation: step.Observation,
			Duration:    step.Duration,
			Success:     step.Success,
			Error:       step.Error,
		})
	}

	rootSnapshots := make([]TraceRootSnapshot, 0, len(trace.RootSnapshots))
	for _, snapshot := range trace.RootSnapshots {
		rootSnapshots = append(rootSnapshots, TraceRootSnapshot{
			Iteration:        snapshot.Iteration,
			PromptTokens:     snapshot.PromptTokens,
			CompletionTokens: snapshot.CompletionTokens,
		})
	}

	return EpisodeTrace{
		StartedAt:      trace.StartedAt,
		CompletedAt:    trace.CompletedAt,
		ProcessingTime: trace.ProcessingTime,
		Iterations:     trace.Iterations,
		Usage: TraceTokenUsage{
			PromptTokens:     trace.Usage.PromptTokens,
			CompletionTokens: trace.Usage.CompletionTokens,
			TotalTokens:      trace.Usage.TotalTokens,
		},
		RootUsage: TraceTokenUsage{
			PromptTokens:     trace.RootUsage.PromptTokens,
			CompletionTokens: trace.RootUsage.CompletionTokens,
			TotalTokens:      trace.RootUsage.TotalTokens,
		},
		SubUsage: TraceTokenUsage{
			PromptTokens:     trace.SubUsage.PromptTokens,
			CompletionTokens: trace.SubUsage.CompletionTokens,
			TotalTokens:      trace.SubUsage.TotalTokens,
		},
		SubRLMUsage: TraceTokenUsage{
			PromptTokens:     trace.SubRLMUsage.PromptTokens,
			CompletionTokens: trace.SubRLMUsage.CompletionTokens,
			TotalTokens:      trace.SubRLMUsage.TotalTokens,
		},
		RootSnapshots:     rootSnapshots,
		SubLLMCallCount:   trace.SubLLMCallCount,
		SubRLMCallCount:   trace.SubRLMCallCount,
		ConfidenceSignals: trace.ConfidenceSignals,
		CompressionCount:  trace.CompressionCount,
		TerminationCause:  trace.TerminationCause,
		Error:             trace.Error,
		Steps:             steps,
	}
}
