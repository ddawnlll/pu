package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamUTF8SplitAcrossChunks(t *testing.T) {
	// Multi-byte UTF-8 characters: Turkish (ğ, ş, ı, ö, ü, ç) and 4-byte emoji (🚀).
	wantText := "Merhaba dünya! Çözüm hazır 🚀 — şğüöçı"
	ssePayload := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Merhaba dünya! \"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Çözüm hazır 🚀 — şğüöçı\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		raw := []byte(ssePayload)
		// Flush 1 byte at a time so every multi-byte UTF-8 sequence is split across HTTP chunks.
		for i := 0; i < len(raw); i++ {
			_, _ = w.Write(raw[i : i+1])
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	client := &OpenAICompatClient{
		Endpoint:   srv.URL,
		APIKey:     "test-key",
		HTTPClient: srv.Client(),
	}

	var streamed strings.Builder
	resp, err := client.Stream(context.Background(), Request{
		Model: "deepseek-v4.1-flash",
		Items: []Item{{Role: RoleUser, Parts: []ContentPart{TextPart{Text: "hi"}}}},
	}, func(ev StreamEvent) {
		if ev.Kind == EventTextDelta {
			streamed.WriteString(ev.Text)
		}
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	if streamed.String() != wantText {
		t.Fatalf("streamed text = %q, want %q", streamed.String(), wantText)
	}
	if len(resp.Item.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(resp.Item.Parts))
	}
	tp, ok := resp.Item.Parts[0].(TextPart)
	if !ok || tp.Text != wantText {
		t.Fatalf("assembled TextPart = %#v, want %q", resp.Item.Parts[0], wantText)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want stop", resp.FinishReason)
	}
}

func TestStreamToolCallArgsSplitAcrossChunks(t *testing.T) {
	chunks := []string{
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"search","arguments":"{\"query\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"internal/core/"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"Hashline\""}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"loop.go\",\"start\":1}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = io.WriteString(w, c+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	client := &OpenAICompatClient{
		Endpoint:   srv.URL,
		HTTPClient: srv.Client(),
	}

	resp, err := client.Stream(context.Background(), Request{
		Model: "deepseek-v4.1-flash",
		Items: []Item{{Role: RoleUser, Parts: []ContentPart{TextPart{Text: "read and search"}}}},
		Tools: []ToolDef{
			{Name: "read", Description: "read file"},
			{Name: "search", Description: "search repo"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("FinishReason = %q, want tool_calls", resp.FinishReason)
	}
	if len(resp.Item.Parts) != 2 {
		t.Fatalf("expected 2 ToolCallParts, got %d: %#v", len(resp.Item.Parts), resp.Item.Parts)
	}

	tc0, ok0 := resp.Item.Parts[0].(ToolCallPart)
	tc1, ok1 := resp.Item.Parts[1].(ToolCallPart)
	if !ok0 || !ok1 {
		t.Fatalf("expected ToolCallPart types, got %T and %T", resp.Item.Parts[0], resp.Item.Parts[1])
	}
	if tc0.ID != "call_1" || tc0.Name != "read" || tc0.Arguments != `{"path":"internal/core/loop.go","start":1}` {
		t.Fatalf("unexpected tc0: %+v", tc0)
	}
	if tc1.ID != "call_2" || tc1.Name != "search" || tc1.Arguments != `{"query":"Hashline"}` {
		t.Fatalf("unexpected tc1: %+v", tc1)
	}
}

func TestStreamUsageAndCachedTokens(t *testing.T) {
	cases := []struct {
		name       string
		usageJSON  string
		wantInput  int
		wantCached int
		wantOutput int
		wantTotal  int
	}{
		{
			name:       "openai prompt_tokens_details.cached_tokens",
			usageJSON:  `{"prompt_tokens":1200,"completion_tokens":300,"total_tokens":1500,"prompt_tokens_details":{"cached_tokens":900}}`,
			wantInput:  1200,
			wantCached: 900,
			wantOutput: 300,
			wantTotal:  1500,
		},
		{
			name:       "deepseek prompt_cache_hit_tokens",
			usageJSON:  `{"prompt_tokens":2000,"completion_tokens":450,"total_tokens":2450,"prompt_cache_hit_tokens":1600}`,
			wantInput:  2000,
			wantCached: 1600,
			wantOutput: 450,
			wantTotal:  2450,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`+"\n\n")
				_, _ = io.WriteString(w, `data: {"choices":[],"usage":`+tc.usageJSON+`}`+"\n\n")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()

			client := &OpenAICompatClient{Endpoint: srv.URL, HTTPClient: srv.Client()}
			var sawUsageEvent bool
			resp, err := client.Stream(context.Background(), Request{
				Model: "deepseek-v4.1-flash",
				Items: []Item{{Role: RoleUser, Parts: []ContentPart{TextPart{Text: "ping"}}}},
			}, func(ev StreamEvent) {
				if ev.Kind == EventUsage && ev.Usage != nil {
					sawUsageEvent = true
				}
			})
			if err != nil {
				t.Fatalf("Stream failed: %v", err)
			}
			if !sawUsageEvent {
				t.Fatalf("expected EventUsage callback")
			}
			if resp.Usage.InputTokens != tc.wantInput ||
				resp.Usage.CachedInputTokens != tc.wantCached ||
				resp.Usage.OutputTokens != tc.wantOutput ||
				resp.Usage.TotalTokens != tc.wantTotal {
				t.Fatalf("Usage = %+v, want input=%d cached=%d output=%d total=%d",
					resp.Usage, tc.wantInput, tc.wantCached, tc.wantOutput, tc.wantTotal)
			}
		})
	}
}

func TestStreamReasoningHeadersAndRequestPayload(t *testing.T) {
	var gotUA, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"reasoning_content":"step 1. "}}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"reasoning_content":"step 2."}}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := &OpenAICompatClient{
		Endpoint:   srv.URL,
		APIKey:     "secret-key",
		UserAgent:  "pu/0.1.0-test",
		HTTPClient: srv.Client(),
	}

	resp, err := client.Stream(context.Background(), Request{
		Model:           "deepseek-v4.1-flash",
		ReasoningParam:  "reasoning_effort",
		ReasoningEffort: "high",
		Items: []Item{
			{Role: RoleSystem, Parts: []ContentPart{TextPart{Text: "sys"}}},
			{Role: RoleUser, Parts: []ContentPart{TextPart{Text: "hi"}}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	if gotUA != "pu/0.1.0-test" {
		t.Fatalf("User-Agent = %q, want pu/0.1.0-test", gotUA)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("Authorization = %q, want Bearer secret-key", gotAuth)
	}
	if gotBody["reasoning_effort"] != "high" {
		t.Fatalf("expected reasoning_effort=high in payload, got %v", gotBody["reasoning_effort"])
	}
	if len(resp.Item.Parts) != 2 {
		t.Fatalf("expected reasoning + text parts, got %#v", resp.Item.Parts)
	}
	rp, ok := resp.Item.Parts[0].(ReasoningPart)
	if !ok || rp.Text != "step 1. step 2." {
		t.Fatalf("unexpected ReasoningPart: %#v", resp.Item.Parts[0])
	}
	tp, ok := resp.Item.Parts[1].(TextPart)
	if !ok || tp.Text != "answer" {
		t.Fatalf("unexpected TextPart: %#v", resp.Item.Parts[1])
	}
}

func TestStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := &OpenAICompatClient{Endpoint: srv.URL, HTTPClient: srv.Client()}
	_, err := client.Stream(context.Background(), Request{
		Model: "deepseek-v4.1-flash",
		Items: []Item{{Role: RoleUser, Parts: []ContentPart{TextPart{Text: "hi"}}}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected HTTP 401 error, got %v", err)
	}
}
