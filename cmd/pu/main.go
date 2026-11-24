package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ddawnlll/pu/internal/catalog"
	"github.com/ddawnlll/pu/internal/core"
	"github.com/ddawnlll/pu/internal/provider"
)

var version = "0.1.0-p0"

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "run":
		return runAgent(args[1:], stdout, stderr)
	case "diff":
		fmt.Fprintln(stderr, "pu diff: not implemented yet (lands in M3)")
		return 2
	case "undo":
		fmt.Fprintln(stderr, "pu undo: not implemented yet (lands in M3)")
		return 2
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "pu %s\n", version)
		return 0
	case "help", "--help", "-h":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "pu: unknown subcommand %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: pu <subcommand> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  run      Run headless coding task")
	fmt.Fprintln(w, "  diff     Show summary of session changes (lands in M3)")
	fmt.Fprintln(w, "  undo     Restore session checkpoints (lands in M3)")
	fmt.Fprintln(w, "  doctor   Check Go version, models.toml, API keys, rg, and env-scrub")
	fmt.Fprintln(w, "  version  Print pu version")
}

func runAgent(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_ = fs.Bool("headless", true, "run in headless mode")
	modelName := fs.String("model", "", "model key from models.toml")
	modelsPath := fs.String("models", "", "path to models.toml (default ~/.pu/models.toml)")
	maxCost := fs.Float64("max-cost-usd", 0, "maximum notional list-price cost in USD")
	maxTurns := fs.Int("max-turns", 0, "maximum agent turns")
	timeout := fs.Duration("timeout", 0, "overall run timeout")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		fmt.Fprintln(stderr, "pu run: prompt argument is required")
		return 2
	}

	path := *modelsPath
	if path == "" {
		if envPath := os.Getenv("PU_MODELS_FILE"); envPath != "" {
			path = envPath
		} else if p, err := catalog.DefaultPath(); err == nil {
			path = p
		}
	}

	cat, err := catalog.Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "pu run: models.toml not found at %s\n", path)
		} else {
			fmt.Fprintf(stderr, "pu run: load models.toml: %v\n", err)
		}
		return 1
	}

	m, err := cat.Model(*modelName)
	if err != nil {
		fmt.Fprintf(stderr, "pu run: %v\n", err)
		return 1
	}
	if m.Driver != catalog.DriverOpenAIChatCompat {
		fmt.Fprintf(stderr, "pu run: driver %q is not enabled in P0 (expected %q)\n", m.Driver, catalog.DriverOpenAIChatCompat)
		return 1
	}

	apiKey, envVar, required, err := m.ResolveAPIKey(os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "pu run: %v\n", err)
		return 1
	}
	if required && apiKey == "" {
		fmt.Fprintf(stderr, "pu run: environment variable %s is not set\n", envVar)
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "pu run: getwd: %v\n", err)
		return 1
	}

	client := &provider.OpenAICompatClient{
		Endpoint:  m.Endpoint,
		APIKey:    apiKey,
		UserAgent: "pu/" + version,
	}

	report, runErr := core.Run(context.Background(), core.Config{
		RepoRoot:   cwd,
		Model:      m,
		MaxCostUSD: *maxCost,
		MaxTurns:   *maxTurns,
		Timeout:    *timeout,
	}, client, prompt)

	if report != nil {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "pu run: %v\n", runErr)
		return 1
	}
	if report != nil && report.Status != core.StatusCompleted {
		return 1
	}
	return 0
}
