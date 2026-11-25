package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ddawnlll/pu/internal/edit"
	puexec "github.com/ddawnlll/pu/internal/exec"
	"github.com/ddawnlll/pu/internal/plan"
	"github.com/ddawnlll/pu/internal/provider"
	"github.com/ddawnlll/pu/internal/search"
)

const (
	SpillThresholdBytes = 64 * 1024 // 64 KiB
	spillHeadBytes      = 8 * 1024
	spillTailBytes      = 4 * 1024
)

// Static tool schemas (Layer 2). Always sorted alphabetically and serialized
// byte-identically on every turn to keep the KV prefix invariant.
var coreToolDefs = []provider.ToolDef{
	{
		Name:        "exec",
		Description: "Run a shell command in the repository (non-interactive pipe, scrubbed env, 64 KiB output cap) and record verification evidence.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"description":"Shell command to execute via /bin/sh -c.","type":"string"},"files":{"description":"Optional repo-relative files covered by this check.","items":{"type":"string"},"type":"array"},"kind":{"description":"Optional check kind: build, lint, targeted_test, full_test.","type":"string"},"step":{"description":"Optional plan step ID backed by this check (e.g. S1).","type":"string"},"timeout_seconds":{"description":"Optional command timeout in seconds (default 60).","type":"integer"}},"required":["command"]}`),
	},
	{
		Name:        "patch",
		Description: "Edit, create, or delete a repository file. Modes: hashline (default, strict 5-letter anchors), search_replace (exactly 1 match), full_rewrite, create, delete.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"content":{"description":"Full file content for full_rewrite or create.","type":"string"},"expected_sha256":{"description":"Optional expected file sha256 (prefix >=8 hex chars or full) for revision lock.","type":"string"},"mode":{"description":"Patch mode: hashline (default), search_replace, full_rewrite, create, delete.","enum":["hashline","search_replace","full_rewrite","create","delete"],"type":"string"},"new_text":{"description":"Replacement text for search_replace.","type":"string"},"old_text":{"description":"Exact target text for search_replace (must match once).","type":"string"},"ops":{"description":"Line-anchored edit operations for hashline mode.","items":{"properties":{"end_anchor":{"type":"string"},"end_line":{"type":"integer"},"insert_after":{"type":"boolean"},"new_text":{"type":"string"},"start_anchor":{"type":"string"},"start_line":{"type":"integer"}},"required":["start_line","start_anchor","new_text"],"type":"object"},"type":"array"},"path":{"description":"Repo-relative file path.","type":"string"}},"required":["path"]}`),
	},
	{
		Name:        "plan",
		Description: "Create or update the short task plan mirrored to .pu/plan.json.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"goal":{"description":"Short task goal.","type":"string"},"status":{"description":"Plan status: in_progress, completed, blocked.","enum":["in_progress","completed","blocked"],"type":"string"},"steps":{"description":"Ordered plan steps.","items":{"properties":{"depends_on":{"items":{"type":"string"},"type":"array"},"files":{"items":{"type":"string"},"type":"array"},"id":{"type":"string"},"check":{"description":"Existing test command that exercises this step's behaviour, if any.","type":"string"},"status":{"enum":["pending","in_progress","done","blocked"],"type":"string"},"title":{"type":"string"}},"required":["id","title","status"],"type":"object"},"type":"array"}},"required":["steps"]}`),
	},
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
	out := make([]provider.ToolDef, len(coreToolDefs))
	copy(out, coreToolDefs)
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

type execArgs struct {
	Command        string   `json:"command"`
	Step           string   `json:"step"`
	Kind           string   `json:"kind"`
	Files          []string `json:"files"`
	TimeoutSeconds int      `json:"timeout_seconds"`
}

type planArgs struct {
	Goal   string      `json:"goal"`
	Status string      `json:"status"`
	Steps  []plan.Step `json:"steps"`
}

type toolExecOutcome struct {
	output        string
	isError       bool
	meta          toolResultMeta
	readOutside   bool
	blockedByGate bool
}

func executeToolCall(
	ctx context.Context,
	repoRoot string,
	tracker *edit.Tracker,
	cp *edit.Checkpointer,
	planMgr *plan.Manager,
	untrustedTainted bool,
	trustUntrusted bool,
	noHashline bool,
	tc provider.ToolCallPart,
) toolExecOutcome {
	switch tc.Name {
	case "exec":
		var args execArgs
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			return toolExecOutcome{output: fmt.Sprintf("error: invalid exec arguments: %v", err), isError: true}
		}
		if untrustedTainted && !trustUntrusted {
			return toolExecOutcome{
				output:        "blocked: untrusted external content entered context; exec is blocked in headless mode unless --trust-untrusted is set",
				isError:       true,
				blockedByGate: true,
			}
		}
		timeout := time.Duration(args.TimeoutSeconds) * time.Second
		res, err := puexec.Run(ctx, puexec.RunOptions{
			Command: args.Command,
			Dir:     repoRoot,
			Timeout: timeout,
		})
		if err != nil {
			return toolExecOutcome{output: fmt.Sprintf("error: %v", err), isError: true}
		}

		// Post-exec stat/hash check on all tracked files; bump revision & stale earlier evidence.
		changedRecs := tracker.CheckExternalMutations()
		if len(changedRecs) > 0 {
			changedPaths := make([]string, len(changedRecs))
			for i, r := range changedRecs {
				changedPaths[i] = r.Path
			}
			planMgr.InvalidateForFiles(changedPaths)
		}

		rev := tracker.WorkspaceRevision()
		ev := planMgr.RecordEvidence(args.Command, res.ExitCode, rev, args.Step, args.Kind, args.Files, "")

		out := res.Format()
		if len(changedRecs) > 0 {
			paths := make([]string, len(changedRecs))
			for i, r := range changedRecs {
				paths[i] = fmt.Sprintf("%s@rev%d", r.Path, r.Revision)
			}
			out += fmt.Sprintf("[exec modified tracked files: %s; prior evidence marked stale]\n", strings.Join(paths, ", "))
		}
		out += fmt.Sprintf("[evidence: %s %s rev%d]\n", ev.ID, ev.Validity, ev.RevisionNum)

		return toolExecOutcome{
			output:  out,
			isError: res.ExitCode != 0,
			meta: toolResultMeta{
				toolName:   "exec",
				cmd:        args.Command,
				evidenceID: ev.ID,
			},
		}

	case "patch":
		var req edit.PatchRequest
		if err := json.Unmarshal([]byte(tc.Arguments), &req); err != nil {
			return toolExecOutcome{output: fmt.Sprintf("error: invalid patch arguments: %v", err), isError: true}
		}
		if noHashline && (req.Mode == "" || req.Mode == edit.ModeHashline) {
			return toolExecOutcome{output: "error: hashline mode disabled by --ablate=hashline; use search_replace or full_rewrite", isError: true}
		}
		res, err := tracker.Patch(cp, req)
		if err != nil {
			return toolExecOutcome{output: fmt.Sprintf("error: %v", err), isError: true}
		}
		planMgr.InvalidateForFiles([]string{res.Path})
		planMgr.MarkEdited()
		return toolExecOutcome{
			output: res.Format(),
			meta:   toolResultMeta{toolName: "patch", path: res.Path},
		}

	case "plan":
		var args planArgs
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			return toolExecOutcome{output: fmt.Sprintf("error: invalid plan arguments: %v", err), isError: true}
		}
		if err := planMgr.Update(args.Goal, args.Steps, args.Status); err != nil {
			return toolExecOutcome{output: fmt.Sprintf("error: update plan: %v", err), isError: true}
		}
		return toolExecOutcome{
			output: planMgr.FormatSummary(),
			meta:   toolResultMeta{toolName: "plan"},
		}

	case "read":
		var args readArgs
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			return toolExecOutcome{output: fmt.Sprintf("error: invalid read arguments: %v", err), isError: true}
		}
		outside := isOutsideRepo(repoRoot, args.Path)
		res, err := tracker.Read(args.Path, args.StartLine, args.EndLine)
		if err != nil {
			return toolExecOutcome{output: fmt.Sprintf("error: %v", err), isError: true}
		}
		return toolExecOutcome{
			output:      res.Format(),
			readOutside: outside,
			meta: toolResultMeta{
				toolName: "read",
				path:     res.Path,
				readRev:  res.Revision,
			},
		}

	case "search":
		var args searchArgs
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			return toolExecOutcome{output: fmt.Sprintf("error: invalid search arguments: %v", err), isError: true}
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
			return toolExecOutcome{output: fmt.Sprintf("error: %v", err), isError: true}
		}
		return toolExecOutcome{
			output: res.Format(),
			meta:   toolResultMeta{toolName: "search"},
		}

	default:
		return toolExecOutcome{output: fmt.Sprintf("error: unknown tool %q", tc.Name), isError: true}
	}
}

func isOutsideRepo(repoRoot, target string) bool {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return false
	}
	absTarget := target
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Join(absRoot, target)
	}
	absTarget = filepath.Clean(absTarget)
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
