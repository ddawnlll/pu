package provider

import (
	"context"
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
