package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeAgentRuntime(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected AgentRuntime
	}{
		{name: "empty defaults", input: "", expected: AgentRuntimeDefault},
		{name: "default", input: "default", expected: AgentRuntimeDefault},
		{name: "rlm", input: "rlm", expected: AgentRuntimeRLM},
		{name: "mixed case", input: "RLM", expected: AgentRuntimeRLM},
		{name: "unknown", input: "something", expected: AgentRuntimeDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeAgentRuntime(tt.input))
		})
	}
}

func TestIsValidAgentRuntime(t *testing.T) {
	assert.True(t, IsValidAgentRuntime("default"))
	assert.True(t, IsValidAgentRuntime("rlm"))
	assert.True(t, IsValidAgentRuntime("RLM"))
	assert.True(t, IsValidAgentRuntime(" default "))
	assert.False(t, IsValidAgentRuntime(""))
	assert.False(t, IsValidAgentRuntime("custom"))
}
