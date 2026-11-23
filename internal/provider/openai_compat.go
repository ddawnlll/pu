package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const DefaultUserAgent = "pu/0.1.0-p0"

// OpenAICompatClient implements streaming chat completions for openai-chat-compat
// endpoints (OpenCode Go in P0).
type OpenAICompatClient struct {
	Endpoint   string
	APIKey     string
	UserAgent  string
	HTTPClient *http.Client
}

// Stream sends a streaming chat completions request, invoking onEvent (if non-nil)
// for each incremental SSE delta, and returns the assembled Response.
func (c *OpenAICompatClient) Stream(ctx context.Context, req Request, onEvent func(StreamEvent)) (*Response, error) {
	if c.Endpoint == "" {
		return nil, errors.New("provider endpoint is empty")
	}
	if req.Model == "" {
		return nil, errors.New("request model is empty")
	}

	bodyBytes, err := buildOpenAIChatRequest(req)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	url := strings.TrimRight(c.Endpoint, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	ua := c.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", ua)
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	httpResp, err := hc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return nil, fmt.Errorf("provider returned HTTP %d: %s", httpResp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	return parseOpenAISSE(httpResp.Body, onEvent)
}

func buildOpenAIChatRequest(req Request) ([]byte, error) {
	messages := make([]map[string]any, 0, len(req.Items))
	for _, item := range req.Items {
		switch item.Role {
		case RoleSystem, RoleUser:
			msg, err := serializeUserOrSystemItem(item)
			if err != nil {
				return nil, err
			}
			messages = append(messages, msg)
		case RoleAssistant:
			messages = append(messages, serializeAssistantItem(item))
		case RoleTool:
			for _, p := range item.Parts {
				tr, ok := p.(ToolResultPart)
				if !ok {
					continue
				}
				m := map[string]any{
					"role":         "tool",
					"tool_call_id": tr.ToolCallID,
					"content":      tr.Content,
				}
				if tr.Name != "" {
					m["name"] = tr.Name
				}
				messages = append(messages, m)
			}
		default:
			return nil, fmt.Errorf("unsupported role %q", item.Role)
		}
	}

	payload := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}

	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	if req.ReasoningParam != "" && req.ReasoningEffort != "" {
		payload[req.ReasoningParam] = req.ReasoningEffort
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, td := range req.Tools {
			params := td.Parameters
			if len(params) == 0 {
				params = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        td.Name,
					"description": td.Description,
					"parameters":  params,
				},
			}
		}
		payload["tools"] = tools
	}

	return json.Marshal(payload)
}

func serializeUserOrSystemItem(item Item) (map[string]any, error) {
	hasImage := false
	for _, p := range item.Parts {
		if _, ok := p.(ImagePart); ok {
			hasImage = true
			break
		}
	}
	if !hasImage {
		var sb strings.Builder
		for _, p := range item.Parts {
			if tp, ok := p.(TextPart); ok {
				sb.WriteString(tp.Text)
			}
		}
		return map[string]any{
			"role":    string(item.Role),
			"content": sb.String(),
		}, nil
	}

	content := make([]map[string]any, 0, len(item.Parts))
	for _, p := range item.Parts {
		switch v := p.(type) {
		case TextPart:
			content = append(content, map[string]any{
				"type": "text",
				"text": v.Text,
			})
		case ImagePart:
			imgURL := v.URL
			if imgURL == "" && len(v.Data) > 0 {
				mime := v.MIMEType
				if mime == "" {
					mime = "image/png"
				}
				imgURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(v.Data)
			}
			content = append(content, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": imgURL,
				},
			})
		}
	}
	return map[string]any{
		"role":    string(item.Role),
		"content": content,
	}, nil
}

func serializeAssistantItem(item Item) map[string]any {
	var textBuf, reasonBuf strings.Builder
	var toolCalls []map[string]any
	for _, p := range item.Parts {
		switch v := p.(type) {
		case TextPart:
			textBuf.WriteString(v.Text)
		case ReasoningPart:
			reasonBuf.WriteString(v.Text)
		case ToolCallPart:
			toolCalls = append(toolCalls, map[string]any{
				"id":   v.ID,
				"type": "function",
				"function": map[string]any{
					"name":      v.Name,
					"arguments": v.Arguments,
				},
			})
		}
	}
	msg := map[string]any{
		"role": "assistant",
	}
	if textBuf.Len() > 0 || len(toolCalls) == 0 {
		msg["content"] = textBuf.String()
	} else {
		msg["content"] = nil
	}
	if reasonBuf.Len() > 0 {
		msg["reasoning_content"] = reasonBuf.String()
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	return msg
}

type sseChunk struct {
	Choices []struct {
		Index        int     `json:"index"`
		FinishReason *string `json:"finish_reason"`
		Delta        struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptCacheHitToken int `json:"prompt_cache_hit_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type toolCallAccum struct {
	id   string
	name string
	args strings.Builder
}

func parseOpenAISSE(body io.Reader, onEvent func(StreamEvent)) (*Response, error) {
	reader := bufio.NewReaderSize(body, 64*1024)

	var textBuf strings.Builder
	var reasonBuf strings.Builder
	toolCalls := make(map[int]*toolCallAccum)
	var usage Usage
	var finishReason string
	var dataLines [][]byte

	flushEvent := func() (done bool, err error) {
		if len(dataLines) == 0 {
			return false, nil
		}
		payload := bytes.Join(dataLines, []byte("\n"))
		dataLines = dataLines[:0]

		trimmed := bytes.TrimSpace(payload)
		if len(trimmed) == 0 {
			return false, nil
		}
		if bytes.Equal(trimmed, []byte("[DONE]")) {
			return true, nil
		}

		var chunk sseChunk
		if err := json.Unmarshal(trimmed, &chunk); err != nil {
			return false, fmt.Errorf("parse SSE chunk: %w", err)
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			return false, fmt.Errorf("provider stream error (%s): %s", chunk.Error.Type, chunk.Error.Message)
		}

		if chunk.Usage != nil {
			cached := chunk.Usage.PromptCacheHitToken
			if chunk.Usage.PromptTokensDetails != nil && chunk.Usage.PromptTokensDetails.CachedTokens > 0 {
				cached = chunk.Usage.PromptTokensDetails.CachedTokens
			}
			usage = Usage{
				InputTokens:       chunk.Usage.PromptTokens,
				CachedInputTokens: cached,
				OutputTokens:      chunk.Usage.CompletionTokens,
				TotalTokens:       chunk.Usage.TotalTokens,
			}
			if onEvent != nil {
				u := usage
				onEvent(StreamEvent{Kind: EventUsage, Usage: &u})
			}
		}

		for _, ch := range chunk.Choices {
			if ch.Delta.ReasoningContent != "" || ch.Delta.Reasoning != "" {
				rText := ch.Delta.ReasoningContent
				if rText == "" {
					rText = ch.Delta.Reasoning
				}
				reasonBuf.WriteString(rText)
				if onEvent != nil {
					onEvent(StreamEvent{Kind: EventReasoningDelta, Text: rText})
				}
			}
			if ch.Delta.Content != "" {
				textBuf.WriteString(ch.Delta.Content)
				if onEvent != nil {
					onEvent(StreamEvent{Kind: EventTextDelta, Text: ch.Delta.Content})
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				acc, ok := toolCalls[tc.Index]
				if !ok {
					acc = &toolCallAccum{}
					toolCalls[tc.Index] = acc
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name += tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.args.WriteString(tc.Function.Arguments)
				}
				if onEvent != nil {
					onEvent(StreamEvent{
						Kind:           EventToolCallDelta,
						ToolCallIndex:  tc.Index,
						ToolCallID:     tc.ID,
						ToolName:       tc.Function.Name,
						ArgumentsDelta: tc.Function.Arguments,
					})
				}
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				finishReason = *ch.FinishReason
			}
		}

		return false, nil
	}

	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fmt.Errorf("read SSE stream: %w", readErr)
		}

		if len(line) > 0 {
			line = bytes.TrimRight(line, "\r\n")
			if len(line) == 0 {
				done, err := flushEvent()
				if err != nil {
					return nil, err
				}
				if done {
					break
				}
			} else if line[0] == ':' {
				// SSE comment; ignore.
			} else if bytes.HasPrefix(line, []byte("data:")) {
				val := line[len("data:"):]
				if len(val) > 0 && val[0] == ' ' {
					val = val[1:]
				}
				// Copy slice since ReadBytes returns a fresh slice per call,
				// but defensive copy keeps dataLines independent.
				cp := make([]byte, len(val))
				copy(cp, val)
				dataLines = append(dataLines, cp)
			}
		}

		if errors.Is(readErr, io.EOF) {
			if _, err := flushEvent(); err != nil {
				return nil, err
			}
			break
		}
	}

	parts := make([]ContentPart, 0, 2+len(toolCalls))
	if reasonBuf.Len() > 0 {
		parts = append(parts, ReasoningPart{Text: reasonBuf.String()})
	}
	if textBuf.Len() > 0 {
		parts = append(parts, TextPart{Text: textBuf.String()})
	}
	if len(toolCalls) > 0 {
		indices := make([]int, 0, len(toolCalls))
		for idx := range toolCalls {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		for _, idx := range indices {
			acc := toolCalls[idx]
			parts = append(parts, ToolCallPart{
				ID:        acc.id,
				Name:      acc.name,
				Arguments: acc.args.String(),
			})
		}
	}

	if onEvent != nil {
		onEvent(StreamEvent{Kind: EventDone, FinishReason: finishReason})
	}

	return &Response{
		Item: Item{
			Role:  RoleAssistant,
			Parts: parts,
		},
		Usage:        usage,
		FinishReason: finishReason,
	}, nil
}
