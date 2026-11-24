package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ToolStats aggregates metrics for one tool across an eval run.
type ToolStats struct {
	Tool                  string
	Total                 int // excludes rate_limited runs
	Passed                int
	Timeouts              int
	RateLimited           int
	SuccessRate           float64
	MedianWallTimeMS      float64
	MedianNotionalCostUSD float64
	MedianInputTokens     float64
	MedianCachedTokens    float64
	MedianOutputTokens    float64
	CostCapEnforced       bool
	TurnCapEnforced       bool
	ReasoningEnforced     bool
	ReasoningNote         string
}

// ReportSummary holds per-tool summaries and the spec §12 Go/No-Go verdict.
type ReportSummary struct {
	ByTool     map[string]ToolStats
	Verdict    string // "GO", "NO-GO", or "INCOMPLETE"
	VerdictMsg string
}

// ReadResults reads all JSONL Result records from path.
func ReadResults(path string) ([]Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Result
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Result
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("parse results.jsonl line: %w", err)
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Summarize computes per-tool metrics (excluding rate_limited runs from pass/fail
// and medians) and the spec §12 P0 -> 0.1 Go/No-Go verdict.
func Summarize(results []Result) ReportSummary {
	grouped := make(map[string][]Result)
	for _, r := range results {
		grouped[r.Tool] = append(grouped[r.Tool], r)
	}

	byTool := make(map[string]ToolStats, len(grouped))
	for tool, rows := range grouped {
		st := ToolStats{Tool: tool}
		var wallTimes, costs, inToks, cacheToks, outToks []float64
		for _, r := range rows {
			if r.Budget.CostCapEnforced {
				st.CostCapEnforced = true
			}
			if r.Budget.TurnCapEnforced {
				st.TurnCapEnforced = true
			}
			if r.ReasoningEnforced {
				st.ReasoningEnforced = true
			}
			if r.ReasoningNote != "" && st.ReasoningNote == "" {
				st.ReasoningNote = r.ReasoningNote
			}
			if r.RateLimited || r.AgentStatus == "rate_limited" {
				st.RateLimited++
				continue
			}
			st.Total++
			if r.TimedOut {
				st.Timeouts++
			}
			if r.Passed {
				st.Passed++
				wallTimes = append(wallTimes, float64(r.WallTimeMS))
				costs = append(costs, r.NotionalCostUSD)
				inToks = append(inToks, float64(r.InputTokens))
				cacheToks = append(cacheToks, float64(r.CachedInputTokens))
				outToks = append(outToks, float64(r.OutputTokens))
			}
		}
		if st.Total > 0 {
			st.SuccessRate = float64(st.Passed) / float64(st.Total)
		}
		st.MedianWallTimeMS = median(wallTimes)
		st.MedianNotionalCostUSD = median(costs)
		st.MedianInputTokens = median(inToks)
		st.MedianCachedTokens = median(cacheToks)
		st.MedianOutputTokens = median(outToks)
		byTool[tool] = st
	}

	verdict, msg := computeVerdict(byTool)
	return ReportSummary{
		ByTool:     byTool,
		Verdict:    verdict,
		VerdictMsg: msg,
	}
}

// Spec §12 Go/No-Go:
// GO if pu success rate >= jcode success rate AND
// (pu median T_task >= 15% faster OR pu median notional cost >= 15% lower).
func computeVerdict(byTool map[string]ToolStats) (string, string) {
	pu, hasPu := byTool["pu"]
	jc, hasJc := byTool["jcode"]
	if !hasPu || !hasJc {
		return "INCOMPLETE", "need both pu and jcode runs to evaluate spec §12 Go/No-Go"
	}
	if pu.Passed == 0 || jc.Passed == 0 {
		return "INCOMPLETE", "both tools need >=1 passing task to compare median wall time and cost"
	}
	if pu.SuccessRate < jc.SuccessRate {
		return "NO-GO", fmt.Sprintf("pu success rate %.1f%% < jcode %.1f%%", pu.SuccessRate*100, jc.SuccessRate*100)
	}

	timeGain := 0.0
	if jc.MedianWallTimeMS > 0 {
		timeGain = (jc.MedianWallTimeMS - pu.MedianWallTimeMS) / jc.MedianWallTimeMS
	}
	costGain := 0.0
	if jc.MedianNotionalCostUSD > 0 {
		costGain = (jc.MedianNotionalCostUSD - pu.MedianNotionalCostUSD) / jc.MedianNotionalCostUSD
	}

	if timeGain >= 0.15 || costGain >= 0.15 {
		return "GO", fmt.Sprintf("pu success %.1f%% >= jcode %.1f%%; median wall-time gain %.1f%%, median notional cost gain %.1f%% (>=15%% threshold met)",
			pu.SuccessRate*100, jc.SuccessRate*100, timeGain*100, costGain*100)
	}
	return "NO-GO", fmt.Sprintf("pu success %.1f%% >= jcode %.1f%%, but neither median wall-time gain (%.1f%%) nor notional cost gain (%.1f%%) reached 15%%",
		pu.SuccessRate*100, jc.SuccessRate*100, timeGain*100, costGain*100)
}

// FormatReport renders a concise human-readable report including budget parity
// notes, reasoning parity notes, and excluded rate-limited runs.
func FormatReport(s ReportSummary) string {
	tools := make([]string, 0, len(s.ByTool))
	for t := range s.ByTool {
		tools = append(tools, t)
	}
	sort.Strings(tools)

	var sb strings.Builder
	sb.WriteString("=== pu P0 Eval Report ===\n")
	for _, t := range tools {
		st := s.ByTool[t]
		fmt.Fprintf(&sb, "%-6s  pass: %d/%d (%.1f%%)  timeouts: %d  median_wall_ms: %.0f  median_notional_cost: $%.6f  median_tokens(in/cached/out): %.0f/%.0f/%.0f",
			st.Tool, st.Passed, st.Total, st.SuccessRate*100, st.Timeouts,
			st.MedianWallTimeMS, st.MedianNotionalCostUSD,
			st.MedianInputTokens, st.MedianCachedTokens, st.MedianOutputTokens)
		if st.RateLimited > 0 {
			fmt.Fprintf(&sb, "  [rate_limited: %d excluded, retryable]", st.RateLimited)
		}
		sb.WriteByte('\n')
	}
	for _, t := range tools {
		st := s.ByTool[t]
		if !st.CostCapEnforced || !st.TurnCapEnforced {
			fmt.Fprintf(&sb, "Budget note (%s): no CLI cost/turn cap enforced (harness wall-clock timeout only)\n", st.Tool)
		}
		if st.ReasoningNote != "" {
			fmt.Fprintf(&sb, "Reasoning note (%s): %s\n", st.Tool, st.ReasoningNote)
		}
	}
	fmt.Fprintf(&sb, "Verdict (spec §12): %s - %s\n", s.Verdict, s.VerdictMsg)
	return sb.String()
}

func median(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	cp := append([]float64(nil), vals...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2.0
}
