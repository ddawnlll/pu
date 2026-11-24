package search

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Status distinguishes normal results, zero matches, and truncated results.
type Status string

const (
	StatusOK        Status = "ok"
	StatusNoMatches Status = "no_matches"
	StatusTruncated Status = "truncated"
)

// Match is one matching line from a search.
type Match struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Text   string `json:"text"`
}

// Result is the structured output of Search.
type Result struct {
	Status  Status  `json:"status"`
	Engine  string  `json:"engine"`
	Matches []Match `json:"matches"`
	Limit   int     `json:"limit"`
}

// Format renders Result in a compact text format for the model.
func (r *Result) Format() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "status: %s engine: %s matches: %d", r.Status, r.Engine, len(r.Matches))
	if r.Status == StatusTruncated {
		fmt.Fprintf(&sb, " (limit %d reached; narrow query or path)", r.Limit)
	}
	sb.WriteByte('\n')
	for _, m := range r.Matches {
		fmt.Fprintf(&sb, "%s:%d:%d: %s\n", m.Path, m.Line, m.Column, m.Text)
	}
	return sb.String()
}

// Options configures a repository search.
type Options struct {
	Root          string
	Query         string
	SubPath       string
	Glob          string
	Regex         bool
	MaxResults    int
	ForceFallback bool
}

const DefaultMaxResults = 100

// Search searches files under opts.Root using rg when available, falling back
// to a pure-Go WalkDir scanner otherwise.
func Search(ctx context.Context, opts Options) (*Result, error) {
	if opts.Query == "" {
		return nil, errors.New("search: query is required")
	}
	if opts.Root == "" {
		opts.Root = "."
	}
	if opts.MaxResults <= 0 {
		opts.MaxResults = DefaultMaxResults
	}

	absRoot, err := filepath.Abs(opts.Root)
	if err != nil {
		return nil, fmt.Errorf("search: resolve root: %w", err)
	}
	opts.Root = absRoot

	target := absRoot
	if opts.SubPath != "" {
		if filepath.IsAbs(opts.SubPath) {
			target = filepath.Clean(opts.SubPath)
		} else {
			target = filepath.Clean(filepath.Join(absRoot, opts.SubPath))
		}
	}

	if !opts.ForceFallback {
		if rgPath, err := osexec.LookPath("rg"); err == nil {
			return searchRG(ctx, rgPath, opts, target)
		}
	}
	return searchWalkDir(ctx, opts, target)
}

func searchRG(ctx context.Context, rgPath string, opts Options, target string) (*Result, error) {
	rgCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := []string{
		"--vimgrep",
		"--no-heading",
		"--color=never",
		"--no-messages",
		"--glob", "!.git/*",
		"--glob", "!.pu/*",
	}
	if !opts.Regex {
		args = append(args, "-F")
	}
	if opts.Glob != "" {
		args = append(args, "--glob", opts.Glob)
	}
	args = append(args, "--", opts.Query, target)

	cmd := osexec.CommandContext(rgCtx, rgPath, args...)
	cmd.Dir = opts.Root

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("rg stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start rg: %w", err)
	}

	matches := make([]Match, 0, min(opts.MaxResults, 32))
	truncated := false

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		m, ok := parseVimgrepLine(opts.Root, scanner.Text())
		if !ok {
			continue
		}
		if len(matches) >= opts.MaxResults {
			truncated = true
			cancel()
			break
		}
		matches = append(matches, m)
	}

	waitErr := cmd.Wait()
	if truncated {
		return &Result{
			Status:  StatusTruncated,
			Engine:  "rg",
			Matches: matches,
			Limit:   opts.MaxResults,
		}, nil
	}

	if waitErr != nil {
		var exitErr *osexec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 && len(matches) == 0 {
			return &Result{
				Status: StatusNoMatches,
				Engine: "rg",
				Limit:  opts.MaxResults,
			}, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("rg failed: %w (%s)", waitErr, strings.TrimSpace(stderr.String()))
	}

	status := StatusOK
	if len(matches) == 0 {
		status = StatusNoMatches
	}
	return &Result{
		Status:  status,
		Engine:  "rg",
		Matches: matches,
		Limit:   opts.MaxResults,
	}, nil
}

func parseVimgrepLine(root, line string) (Match, bool) {
	// Format: path:line:col:text
	first := strings.IndexByte(line, ':')
	if first <= 0 {
		return Match{}, false
	}
	rest := line[first+1:]
	second := strings.IndexByte(rest, ':')
	if second <= 0 {
		return Match{}, false
	}
	lineNum, err := strconv.Atoi(rest[:second])
	if err != nil {
		return Match{}, false
	}
	rest = rest[second+1:]
	third := strings.IndexByte(rest, ':')
	if third <= 0 {
		return Match{}, false
	}
	colNum, err := strconv.Atoi(rest[:third])
	if err != nil {
		return Match{}, false
	}
	rawPath := line[:first]
	displayPath := rawPath
	if filepath.IsAbs(rawPath) {
		if rel, err := filepath.Rel(root, rawPath); err == nil {
			displayPath = rel
		}
	}
	return Match{
		Path:   filepath.ToSlash(displayPath),
		Line:   lineNum,
		Column: colNum,
		Text:   rest[third+1:],
	}, true
}

var errLimitReached = errors.New("search limit reached")

func searchWalkDir(ctx context.Context, opts Options, target string) (*Result, error) {
	var re *regexp.Regexp
	if opts.Regex {
		var err error
		re, err = regexp.Compile(opts.Query)
		if err != nil {
			return nil, fmt.Errorf("search: invalid regex: %w", err)
		}
	}

	matches := make([]Match, 0, min(opts.MaxResults, 32))
	truncated := false

	walkErr := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".pu" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if opts.Glob != "" {
			ok, mErr := filepath.Match(opts.Glob, name)
			if mErr != nil {
				return fmt.Errorf("search: invalid glob %q: %w", opts.Glob, mErr)
			}
			if !ok {
				return nil
			}
		}

		data, rErr := os.ReadFile(path)
		if rErr != nil || isBinary(data) {
			return nil
		}

		displayPath := path
		if rel, err := filepath.Rel(opts.Root, path); err == nil {
			displayPath = filepath.ToSlash(rel)
		}

		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for idx, line := range lines {
			col := -1
			if opts.Regex {
				if loc := re.FindStringIndex(line); loc != nil {
					col = loc[0] + 1
				}
			} else {
				if pos := strings.Index(line, opts.Query); pos >= 0 {
					col = pos + 1
				}
			}
			if col < 0 {
				continue
			}
			if len(matches) >= opts.MaxResults {
				truncated = true
				return errLimitReached
			}
			matches = append(matches, Match{
				Path:   displayPath,
				Line:   idx + 1,
				Column: col,
				Text:   line,
			})
		}
		return nil
	})

	if walkErr != nil && !errors.Is(walkErr, errLimitReached) {
		return nil, walkErr
	}

	status := StatusOK
	if truncated {
		status = StatusTruncated
	} else if len(matches) == 0 {
		status = StatusNoMatches
	}
	return &Result{
		Status:  status,
		Engine:  "go",
		Matches: matches,
		Limit:   opts.MaxResults,
	}, nil
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
