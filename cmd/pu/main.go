package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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
	fmt.Fprintln(w, "  run      Run headless coding task (agent loop lands in M2)")
	fmt.Fprintln(w, "  diff     Show summary of session changes (lands in M3)")
	fmt.Fprintln(w, "  undo     Restore session checkpoints (lands in M3)")
	fmt.Fprintln(w, "  doctor   Check Go version, models.toml, API keys, rg, and env-scrub")
	fmt.Fprintln(w, "  version  Print pu version")
}

func runAgent(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	_ = fs.Bool("headless", true, "run in headless mode")
	_ = fs.String("model", "", "model key from models.toml")
	_ = fs.String("models", "", "path to models.toml (default ~/.pu/models.toml)")
	_ = fs.Float64("max-cost-usd", 0, "maximum spend in USD")
	_ = fs.Int("max-turns", 0, "maximum agent turns")
	_ = fs.Duration("timeout", 0, "overall run timeout")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	fmt.Fprintln(stderr, "pu run: agent loop not implemented yet (lands in M2)")
	return 2
}
