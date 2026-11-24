package core

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ddawnlll/pu/internal/catalog"
	"github.com/ddawnlll/pu/internal/provider"
)

func TestFourLayerPrefixByteIdenticalAcrossTurns(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("Rule 1: keep diffs minimal."), 0o644); err != nil {
		t.Fatalf("WriteFile AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/demo\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package demo\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.go"), []byte("package demo\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile b.go: %v", err)
	}

	var mu sync.Mutex
	var rawRequests [][]byte
	turn := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		turn++
		cur := turn
		rawRequests = append(rawRequests, body)
		mu.Unlock()

		// Mutate AGENTS.md after turn 1 to prove Layer 3 is pinned at session start.
		if cur == 1 {
			_ = os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("Rule 1: MUTATED MID SESSION"), 0o644)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		switch cur {
		case 1:
			_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.go\"}"}},{"index":1,"id":"c2","type":"function","function":{"name":"read","arguments":"{\"path\":\"b.go\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120}}`+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		case 2:
			_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c3","type":"function","function":{"name":"search","arguments":"{\"query\":\"func B\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"choices":[],"usage":{"prompt_tokens":200,"completion_tokens":15,"total_tokens":215,"prompt_tokens_details":{"cached_tokens":100}}}`+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"All inspected."},"finish_reason":"stop"}]}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"choices":[],"usage":{"prompt_tokens":250,"completion_tokens":10,"total_tokens":260,"prompt_tokens_details":{"cached_tokens":200}}}`+"\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}
	}))
	defer srv.Close()

	client := &provider.OpenAICompatClient{
		Endpoint:   srv.URL,
		APIKey:     "fake",
		HTTPClient: srv.Client(),
	}

	cachedPrice := 0.003
	rep, err := Run(context.Background(), Config{
		RepoRoot:  repo,
		SessionID: "sess-kv-test",
		Model: catalog.Model{
			ID:                   "deepseek-v4.1-flash",
			Driver:               catalog.DriverOpenAIChatCompat,
			InputPricePerM:       0.15,
			CachedInputPricePerM: &cachedPrice,
			OutputPricePerM:      0.60,
		},
	}, client, "Inspect a.go and b.go")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if rep.Status != StatusCompleted || rep.Turns != 3 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if rep.Tokens.InputTokens != 550 || rep.Tokens.CachedInputTokens != 300 || rep.Tokens.OutputTokens != 45 {
		t.Fatalf("unexpected raw tokens: %+v", rep.Tokens)
	}

	if len(rawRequests) != 3 {
		t.Fatalf("expected 3 HTTP requests, got %d", len(rawRequests))
	}

	type wireReq struct {
		Messages []json.RawMessage `json:"messages"`
		Tools    json.RawMessage   `json:"tools"`
	}

	var parsed [3]wireReq
	for i, raw := range rawRequests {
		if err := json.Unmarshal(raw, &parsed[i]); err != nil {
			t.Fatalf("unmarshal req %d: %v", i, err)
		}
		if len(parsed[i].Messages) < 3 {
			t.Fatalf("req %d has fewer than 3 messages: %d", i, len(parsed[i].Messages))
		}
	}

	// Prove Layer 1 (messages[0]), Layer 2 (tools), and Layer 3 (messages[1]) are byte-identical on every request.
	for i := 1; i < 3; i++ {
		if !bytes.Equal(parsed[0].Messages[0], parsed[i].Messages[0]) {
			t.Fatalf("Layer 1 drifted between req 0 and req %d:\n%s\nvs\n%s", i, parsed[0].Messages[0], parsed[i].Messages[0])
		}
		if !bytes.Equal(parsed[0].Tools, parsed[i].Tools) {
			t.Fatalf("Layer 2 (tools) drifted between req 0 and req %d:\n%s\nvs\n%s", i, parsed[0].Tools, parsed[i].Tools)
		}
		if !bytes.Equal(parsed[0].Messages[1], parsed[i].Messages[1]) {
			t.Fatalf("Layer 3 (AGENTS.md) drifted between req 0 and req %d:\n%s\nvs\n%s", i, parsed[0].Messages[1], parsed[i].Messages[1])
		}
	}

	// Verify Turn-0 fingerprint is in first user message (messages[2]) and NOT in Layer 1 or Layer 3.
	if strings.Contains(string(parsed[0].Messages[0]), "turn0_fingerprint") {
		t.Fatalf("Turn-0 fingerprint leaked into Layer 1 system prompt")
	}
	if strings.Contains(string(parsed[0].Messages[1]), "turn0_fingerprint") {
		t.Fatalf("Turn-0 fingerprint leaked into Layer 3 project rules")
	}
	var firstUser struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(parsed[0].Messages[2], &firstUser); err != nil {
		t.Fatalf("unmarshal first user message: %v", err)
	}
	if firstUser.Role != "user" || !strings.Contains(firstUser.Content, "<turn0_fingerprint>") ||
		!strings.Contains(firstUser.Content, "go.mod") {
		t.Fatalf("expected Turn-0 fingerprint with go.mod in first user turn: %+v", firstUser)
	}

	// Verify parallel reads in Turn 1 came back together in ONE tool-result turn before Turn 2:
	// Request 1 messages: [sys1, sys3, user0, assistant1(2 tool calls), tool(c1), tool(c2)] = 6 messages.
	if len(parsed[1].Messages) != 6 {
		t.Fatalf("expected 6 messages on Turn 2 (both parallel reads in one turn), got %d", len(parsed[1].Messages))
	}
	if !strings.Contains(string(parsed[1].Messages[4]), `"tool_call_id":"c1"`) ||
		!strings.Contains(string(parsed[1].Messages[5]), `"tool_call_id":"c2"`) {
		t.Fatalf("expected c1 and c2 tool results in order, got:\n%s\n%s", parsed[1].Messages[4], parsed[1].Messages[5])
	}
}

func TestOutputSpillOver64KiB(t *testing.T) {
	repo := t.TempDir()
	// Create a file > 64 KiB (500 lines * ~160 bytes = ~80 KiB)
	var big strings.Builder
	for i := 1; i <= 500; i++ {
		big.WriteString(strings.Repeat("x", 160))
		big.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(repo, "big.txt"), []byte(big.String()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var capturedToolResult string
	turn := 0
	fake := fakeStreamerFunc(func(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
		turn++
		if turn == 1 {
			return &provider.Response{
				FinishReason: "tool_calls",
				Item: provider.Item{
					Role: provider.RoleAssistant,
					Parts: []provider.ContentPart{
						provider.ToolCallPart{ID: "call_big", Name: "read", Arguments: `{"path":"big.txt"}`},
					},
				},
			}, nil
		}
		last := req.Items[len(req.Items)-1]
		capturedToolResult = last.Parts[0].(provider.ToolResultPart).Content
		return &provider.Response{
			FinishReason: "stop",
			Item:         provider.Item{Role: provider.RoleAssistant, Parts: []provider.ContentPart{provider.TextPart{Text: "done"}}},
		}, nil
	})

	rep, err := Run(context.Background(), Config{
		RepoRoot:  repo,
		SessionID: "sess-spill",
		Model:     catalog.Model{ID: "deepseek-v4.1-flash"},
	}, fake, "read big.txt")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", rep.Status)
	}
	if !strings.Contains(capturedToolResult, "[output spilled:") {
		t.Fatalf("expected spilled excerpt marker in tool result, got len=%d", len(capturedToolResult))
	}
	spillFile := filepath.Join(repo, ".pu", "sessions", "sess-spill", "out", "call_big.txt")
	data, err := os.ReadFile(spillFile)
	if err != nil {
		t.Fatalf("expected spill file at %s: %v", spillFile, err)
	}
	if len(data) <= SpillThresholdBytes {
		t.Fatalf("expected spilled file > 64 KiB, got %d bytes", len(data))
	}
}

func TestBudgetExhausted(t *testing.T) {
	t.Run("max_turns", func(t *testing.T) {
		repo := t.TempDir()
		_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hello\n"), 0o644)
		fake := fakeStreamerFunc(func(ctx context.Context, req provider.Request, _ func(provider.StreamEvent)) (*provider.Response, error) {
			return &provider.Response{
				FinishReason: "tool_calls",
				Item: provider.Item{
					Role:  provider.RoleAssistant,
					Parts: []provider.ContentPart{provider.ToolCallPart{ID: "c", Name: "read", Arguments: `{"path":"f.txt"}`}},
				},
			}, nil
		})
		rep, err := Run(context.Background(), Config{
			RepoRoot: repo,
			MaxTurns: 2,
			Model:    catalog.Model{ID: "deepseek-v4.1-flash"},
		}, fake, "loop")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Status != StatusBudgetExhausted || rep.Turns != 2 {
			t.Fatalf("got status=%s turns=%d, want budget_exhausted turns=2", rep.Status, rep.Turns)
		}
	})

	t.Run("max_cost_usd", func(t *testing.T) {
		repo := t.TempDir()
		fake := fakeStreamerFunc(func(ctx context.Context, req provider.Request, _ func(provider.StreamEvent)) (*provider.Response, error) {
			return &provider.Response{
				FinishReason: "stop",
				Usage:        provider.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, TotalTokens: 2_000_000},
				Item:         provider.Item{Role: provider.RoleAssistant, Parts: []provider.ContentPart{provider.TextPart{Text: "done"}}},
			}, nil
		})
		rep, err := Run(context.Background(), Config{
			RepoRoot:   repo,
			MaxCostUSD: 0.10,
			Model:      catalog.Model{ID: "deepseek-v4.1-flash", InputPricePerM: 0.15, OutputPricePerM: 0.60},
		}, fake, "expensive")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Status != StatusBudgetExhausted {
			t.Fatalf("got status=%s, want budget_exhausted (must never report completed)", rep.Status)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		repo := t.TempDir()
		fake := fakeStreamerFunc(func(ctx context.Context, req provider.Request, _ func(provider.StreamEvent)) (*provider.Response, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		rep, err := Run(context.Background(), Config{
			RepoRoot: repo,
			Timeout:  30 * time.Millisecond,
			Model:    catalog.Model{ID: "deepseek-v4.1-flash"},
		}, fake, "slow")
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Status != StatusBudgetExhausted {
			t.Fatalf("got status=%s, want budget_exhausted on timeout", rep.Status)
		}
	})
}

type fakeStreamerFunc func(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error)

func (f fakeStreamerFunc) Stream(ctx context.Context, req provider.Request, onEvent func(provider.StreamEvent)) (*provider.Response, error) {
	return f(ctx, req, onEvent)
}
