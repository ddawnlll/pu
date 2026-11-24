package provider

import "encoding/json"

// Role identifies the speaker/origin of a conversation Item.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// PartKind identifies the concrete variant of a ContentPart.
type PartKind string

const (
	PartText       PartKind = "text"
	PartImage      PartKind = "image"
	PartVideo      PartKind = "video"
	PartAudio      PartKind = "audio"
	PartFileRef    PartKind = "file_ref"
	PartToolCall   PartKind = "tool_call"
	PartToolResult PartKind = "tool_result"
	PartReasoning  PartKind = "reasoning"
)

// ContentPart is the typed union of message content parts (spec §07-B).
// VideoPart, AudioPart, and FileRefPart are declared for 0.2 compatibility
// and unused in P0.
type ContentPart interface {
	Kind() PartKind
	contentPart()
}

type TextPart struct {
	Text string
}

func (TextPart) Kind() PartKind { return PartText }
func (TextPart) contentPart()   {}

type ImagePart struct {
	URL      string
	MIMEType string
	Data     []byte
}

func (ImagePart) Kind() PartKind { return PartImage }
func (ImagePart) contentPart()   {}

type VideoPart struct {
	URL      string
	MIMEType string
	FileURI  string
}

func (VideoPart) Kind() PartKind { return PartVideo }
func (VideoPart) contentPart()   {}

type AudioPart struct {
	URL      string
	MIMEType string
	Data     []byte
}

func (AudioPart) Kind() PartKind { return PartAudio }
func (AudioPart) contentPart()   {}

type FileRefPart struct {
	FileURI  string
	MIMEType string
	Name     string
}

func (FileRefPart) Kind() PartKind { return PartFileRef }
func (FileRefPart) contentPart()   {}

type ToolCallPart struct {
	ID        string
	Name      string
	Arguments string
}

func (ToolCallPart) Kind() PartKind { return PartToolCall }
func (ToolCallPart) contentPart()   {}

type ToolResultPart struct {
	ToolCallID string
	Name       string
	Content    string
	IsError    bool
}

func (ToolResultPart) Kind() PartKind { return PartToolResult }
func (ToolResultPart) contentPart()   {}

type ReasoningPart struct {
	Text string
}

func (ReasoningPart) Kind() PartKind { return PartReasoning }
func (ReasoningPart) contentPart()   {}

// Item is one turn/message in the conversation history.
type Item struct {
	Role  Role
	Parts []ContentPart
}

// ToolDef describes one tool schema exposed to the model.
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Usage holds normalized token counts for one provider response.
type Usage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`
}

// Request is a single streaming completion request.
type Request struct {
	Model           string
	SessionID       string
	Items           []Item
	Tools           []ToolDef
	ReasoningParam  string
	ReasoningEffort string
	MaxTokens       int
}

// Response is the assembled result of a streaming completion call.
type Response struct {
	Item         Item
	Usage        Usage
	FinishReason string
}

// StreamEventKind identifies a streaming delta event.
type StreamEventKind string

const (
	EventTextDelta      StreamEventKind = "text_delta"
	EventReasoningDelta StreamEventKind = "reasoning_delta"
	EventToolCallDelta  StreamEventKind = "tool_call_delta"
	EventUsage          StreamEventKind = "usage"
	EventDone           StreamEventKind = "done"
)

// StreamEvent represents one incremental event emitted during streaming.
type StreamEvent struct {
	Kind           StreamEventKind
	Text           string
	ToolCallIndex  int
	ToolCallID     string
	ToolName       string
	ArgumentsDelta string
	Usage          *Usage
	FinishReason   string
}
