package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ddawnlll/pu/eval"
)

func main() {
	tasksDir := flag.String("tasks", "eval/sample_tasks", "directory containing task subdirectories with task.toml")
	outPath := flag.String("out", "eval/results.jsonl", "path to append JSONL results")
	model := flag.String("model", "deepseek-v4.1-flash", "model key/id")
	modelsFile := flag.String("models", "", "path to models.toml for pu")
	reasoningEffort := flag.String("reasoning-effort", "provider_default", "reasoning effort (\"provider_default\" uses provider default for both tools)")
	maxCost := flag.Float64("max-cost-usd", 0.50, "notional cost cap per task in USD")
	maxTurns := flag.Int("max-turns", 20, "max turns per task")
	timeout := flag.Duration("timeout", 3*time.Minute, "timeout per task run")
	puBin := flag.String("pu-bin", "./pu", "path to pu binary")
	jcodeBin := flag.String("jcode-bin", "jcode", "path to jcode binary")
	toolsCSV := flag.String("tools", "pu,jcode", "comma-separated tools to run (e.g. pu,jcode or pu)")
	ablate := flag.String("ablate", "", "optional hidden --ablate flags passed to pu")
	resume := flag.Bool("resume", false, "skip (task_id, tool) pairs already completed (non-rate_limited) in --out")
	reportOnly := flag.Bool("report-only", false, "only print report from existing --out results.jsonl")
	flag.Parse()

	if !*reportOnly {
		var tools []string
		for _, tok := range strings.Split(*toolsCSV, ",") {
			if t := strings.TrimSpace(tok); t != "" {
				tools = append(tools, t)
			}
		}
		_, err := eval.RunAll(context.Background(), eval.RunConfig{
			TasksDir:        *tasksDir,
			ResultsPath:     *outPath,
			Model:           *model,
			ModelsFile:      *modelsFile,
			ReasoningEffort: *reasoningEffort,
			MaxCostUSD:      *maxCost,
			MaxTurns:        *maxTurns,
			Timeout:         *timeout,
			PuBin:           *puBin,
			JcodeBin:        *jcodeBin,
			Tools:           tools,
			Ablate:          *ablate,
			Resume:          *resume,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "pu-eval: %v\n", err)
			os.Exit(1)
		}
	}

	results, err := eval.ReadResults(*outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pu-eval read results: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(eval.FormatReport(eval.Summarize(results)))
}
