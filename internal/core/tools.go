package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ddawnlll/pu/internal/edit"
	"github.com/ddawnlll/pu/internal/provider"
	"github.com/ddawnlll/pu/internal/search"
)

const (
	SpillThresholdBytes = 64 * 1024 // 64 KiB
	spillHeadBytes      = 8 * 1024
	spillTailBytes      = 4 * 1024
)

// Static M2 tool schemas (Layer 2). Always sorted alphabetically and serialized
// byte-identically on every turn to keep the KV prefix invariant.
var m2ToolDefs = []provider.ToolDef{
	{
		Name:        "read",
		Description: "Read a file with unique 5-letter line anchors and its revision/hash.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"end_line":{"description":"Optional 1-based inclusive end line.","type":"integer"},"path":{"description":"Path to the file (relative to repo root or absolute).","type":"string"},"start_line":{"description":"Optional 1-based start line (default 1).","type":"integer"}},"required":["path"]}`),
	},
	{
		Name:        "search",
		Description: "Search repository files for a literal string or regex using rg (with Go WalkDir fallback).",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"glob":{"description":"Optional filename glob filter (e.g. *.go).","type":"string"},"max_results":{"description":"Maximum matches to return (default 100).","type":"integer"},"path":{"description":"Optional relative directory or file to narrow search.","type":"string"},"query":{"description":"Search pattern.","type":"string"},"regex":{"description":"Treat query as regular expression (default false, literal match).","type":"boolean"}},"required":["query"]}`),
	},
}

// ToolDefs returns the alphabetically sorted tool definitions for Layer 2.
func ToolDefs() []provider.ToolDef {
	out := make([]provider.ToolDef, len(m2ToolDefs))
	copy(out, m2ToolDefs)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type readArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type searchArgs struct {
	Query      string `json:"query"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	Regex      bool   `json:"regex"`
	MaxResults int    `json:"max_results"`
}

func executeToolCall(ctx context.Context, repoRoot string, tracker *edit.Tracker, tc provider.ToolCallPart) (string, bool) {
	switch tc.Name {
	case "read":
		var args readArgs
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			return fmt.Sprintf("error: invalid read arguments: %v", err), true
		}
		res, err := tracker.Read(args.Path, args.StartLine, args.EndLine)
		if err != nil {
			return fmt.Sprintf("error: %v", err), true
		}
		return res.Format(), false

	case "search":
		var args searchArgs
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			return fmt.Sprintf("error: invalid search arguments: %v", err), true
		}
		res, err := search.Search(ctx, search.Options{
			Root:       repoRoot,
			Query:      args.Query,
			SubPath:    args.Path,
			Glob:       args.Glob,
			Regex:      args.Regex,
			MaxResults: args.MaxResults,
		})
		if err != nil {
			return fmt.Sprintf("error: %v", err), true
		}
		return res.Format(), false

	default:
		return fmt.Sprintf("error: unknown tool %q", tc.Name), true
	}
}

// SpillOutput writes outputs larger than 64 KiB to .pu/sessions/<sessionID>/out/<callID>.txt
// and returns a compact excerpt with a reference path so the model can read slices on demand.
func SpillOutput(repoRoot, sessionID, callID, output string) (content string, spilledRelPath string, err error) {
	if len(output) <= SpillThresholdBytes {
		return output, "", nil
	}

	safeID := sanitizeFilename(callID)
	if safeID == "" {
		safeID = "output"
	}
	relPath := filepath.ToSlash(filepath.Join(".pu", "sessions", sessionID, "out", safeID+".txt"))
	absPath := filepath.Join(repoRoot, filepath.FromSlash(relPath))

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", "", fmt.Errorf("create spill dir: %w", err)
	}
	if err := os.WriteFile(absPath, []byte(output), 0o644); err != nil {
		return "", "", fmt.Errorf("write spill file: %w", err)
	}

	lineCount := strings.Count(output, "\n") + 1
	head := trimUTF8Prefix(output, spillHeadBytes)
	tail := trimUTF8Suffix(output, spillTailBytes)

	excerpt := fmt.Sprintf(
		"%s\n\n[output spilled: %d bytes (%d lines) written to %s; use read tool with start_line/end_line to inspect]\n\n%s",
		head, len(output), lineCount, relPath, tail,
	)
	return excerpt, relPath, nil
}

func sanitizeFilename(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteByte('_')
		}
	}
	return sb.String()
}

func trimUTF8Prefix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Back up to a valid UTF-8 rune boundary.
	i := maxBytes
	for i > 0 && (s[i]&0xc0) == 0x80 {
		i--
	}
	return s[:i]
}

func trimUTF8Suffix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	i := len(s) - maxBytes
	for i < len(s) && (s[i]&0xc0) == 0x80 {
		i++
	}
	return s[i:]
}
