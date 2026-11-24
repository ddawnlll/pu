package provider

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ddawnlll/pu/internal/catalog"
)

// TestSmokeOpenCodeGo runs a live streaming request against OpenCode Go when
// OPENCODE_API_KEY is set in the environment. Skips cleanly otherwise.
func TestSmokeOpenCodeGo(t *testing.T) {
	key := os.Getenv("OPENCODE_API_KEY")
	if key == "" {
		t.Skip("OPENCODE_API_KEY not set; skipping live smoke test")
	}

	model := os.Getenv("PU_SMOKE_MODEL")
	if model == "" {
		model = "deepseek-v4.1-flash"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := &OpenAICompatClient{
		Endpoint: catalog.DefaultEndpointOpenCodeGo,
		APIKey:   key,
	}

	resp, err := client.Stream(ctx, Request{
		Model:           model,
		SessionID:       "pu-smoke-test",
		ReasoningParam:  "reasoning_effort",
		ReasoningEffort: "low",
		MaxTokens:       64,
		Items: []Item{
			{Role: RoleUser, Parts: []ContentPart{TextPart{Text: "Reply with the single word: pong"}}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("OpenCode Go smoke call failed: %v", err)
	}

	var gotText strings.Builder
	for _, p := range resp.Item.Parts {
		if tp, ok := p.(TextPart); ok {
			gotText.WriteString(tp.Text)
		}
	}
	if strings.TrimSpace(gotText.String()) == "" {
		t.Fatalf("expected non-empty text response, got %#v", resp.Item.Parts)
	}
	if resp.Usage.InputTokens == 0 || resp.Usage.OutputTokens == 0 {
		t.Fatalf("expected non-zero token usage, got %+v", resp.Usage)
	}
	t.Logf("smoke ok: model=%s text=%q usage=%+v", model, strings.TrimSpace(gotText.String()), resp.Usage)
}

// TestSmokeTwoTurnToolCallReasoningHistory runs a live two-turn tool-call flow
// against OpenCode Go (deepseek-v4.1-flash, reasoning_effort=low) to verify how
// reasoning_content in assistant history is handled (required, accepted, or rejected).
func TestSmokeTwoTurnToolCallReasoningHistory(t *testing.T) {
	key := os.Getenv("OPENCODE_API_KEY")
	if key == "" {
		t.Skip("OPENCODE_API_KEY not set; skipping live two-turn smoke test")
	}

	model := os.Getenv("PU_SMOKE_MODEL")
	if model == "" {
		model = "deepseek-v4.1-flash"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := &OpenAICompatClient{
		Endpoint: catalog.DefaultEndpointOpenCodeGo,
		APIKey:   key,
	}

	tools := []ToolDef{
		{
			Name:        "get_codeword",
			Description: "Returns the secret codeword for verification.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"slot":{"type":"string"}},"required":["slot"]}`),
		},
	}

	userItem := Item{
		Role:  RoleUser,
		Parts: []ContentPart{TextPart{Text: "Call get_codeword with slot \"alpha\", then reply with only the returned codeword."}},
	}

	// Turn 1: model should issue a tool call (and may emit reasoning_content).
	resp1, err := client.Stream(ctx, Request{
		Model:           model,
		SessionID:       "pu-smoke-two-turn-1",
		Turn:            1,
		ReasoningParam:  "reasoning_effort",
		ReasoningEffort: "low",
		MaxTokens:       256,
		Items:           []Item{userItem},
		Tools:           tools,
	}, nil)
	if err != nil {
		t.Fatalf("turn 1 failed: %v", err)
	}

	var toolCall *ToolCallPart
	var hasReasoning bool
	for _, p := range resp1.Item.Parts {
		switch v := p.(type) {
		case ToolCallPart:
			cp := v
			toolCall = &cp
		case ReasoningPart:
			if v.Text != "" {
				hasReasoning = true
			}
		}
	}
	if toolCall == nil {
		t.Fatalf("turn 1 did not emit a tool call; parts=%#v", resp1.Item.Parts)
	}
	t.Logf("turn 1 ok: tool=%s id=%s args=%s hasReasoning=%v usage=%+v",
		toolCall.Name, toolCall.ID, toolCall.Arguments, hasReasoning, resp1.Usage)

	toolResultItem := Item{
		Role: RoleTool,
		Parts: []ContentPart{
			ToolResultPart{
				ToolCallID: toolCall.ID,
				Name:       toolCall.Name,
				Content:    `{"codeword":"ORCHID-42"}`,
			},
		},
	}

	// Turn 2A: send history WITH non-empty reasoning_content preserved.
	withReasoningItem := resp1.Item
	if !hasReasoning {
		parts := append([]ContentPart{ReasoningPart{Text: "I should call get_codeword for slot alpha."}}, resp1.Item.Parts...)
		withReasoningItem = Item{Role: RoleAssistant, Parts: parts}
	}
	resp2With, errWith := client.Stream(ctx, Request{
		Model:           model,
		SessionID:       "pu-smoke-two-turn-1",
		Turn:            2,
		ReasoningParam:  "reasoning_effort",
		ReasoningEffort: "low",
		MaxTokens:       128,
		Items:           []Item{userItem, withReasoningItem, toolResultItem},
		Tools:           tools,
	}, nil)
	if errWith != nil {
		t.Logf("turn 2 WITH reasoning_content: ERROR: %v", errWith)
	} else {
		var txt strings.Builder
		for _, p := range resp2With.Item.Parts {
			if tp, ok := p.(TextPart); ok {
				txt.WriteString(tp.Text)
			}
		}
		t.Logf("turn 2 WITH reasoning_content: OK text=%q usage=%+v", strings.TrimSpace(txt.String()), resp2With.Usage)
	}

	// Turn 2B: send history WITHOUT reasoning_content (strip ReasoningPart).
	strippedParts := make([]ContentPart, 0, len(resp1.Item.Parts))
	for _, p := range resp1.Item.Parts {
		if _, isR := p.(ReasoningPart); !isR {
			strippedParts = append(strippedParts, p)
		}
	}
	strippedAssistant := Item{Role: RoleAssistant, Parts: strippedParts}

	resp2Without, errWithout := client.Stream(ctx, Request{
		Model:           model,
		SessionID:       "pu-smoke-two-turn-2",
		Turn:            2,
		ReasoningParam:  "reasoning_effort",
		ReasoningEffort: "low",
		MaxTokens:       128,
		Items:           []Item{userItem, strippedAssistant, toolResultItem},
		Tools:           tools,
	}, nil)
	if errWithout != nil {
		t.Logf("turn 2 WITHOUT reasoning_content: ERROR: %v", errWithout)
	} else {
		var txt strings.Builder
		for _, p := range resp2Without.Item.Parts {
			if tp, ok := p.(TextPart); ok {
				txt.WriteString(tp.Text)
			}
		}
		t.Logf("turn 2 WITHOUT reasoning_content: OK text=%q usage=%+v", strings.TrimSpace(txt.String()), resp2Without.Usage)
	}

	if errWith != nil && errWithout != nil {
		t.Fatalf("both turn-2 variants failed: with=%v, without=%v", errWith, errWithout)
	}
}
