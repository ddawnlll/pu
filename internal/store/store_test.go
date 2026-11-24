package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJournalAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	path := SessionPath(dir, "sess-1")

	j, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ev1, err := j.Append("session_start", map[string]string{"model": "deepseek-v4.1-flash"})
	if err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	ev2, err := j.Append("assistant_text", map[string]string{"text": "merhaba dünya"})
	if err != nil {
		t.Fatalf("Append 2: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if ev1.Seq != 1 || ev2.Seq != 2 {
		t.Fatalf("unexpected seqs: %d, %d", ev1.Seq, ev2.Seq)
	}

	events, truncated, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if truncated {
		t.Fatalf("expected truncated=false")
	}
	if len(events) != 2 || events[0].Type != "session_start" || events[1].Type != "assistant_text" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestTruncatedLastLineRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	j, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := j.Append("turn_start", map[string]int{"turn": 1}); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	if _, err := j.Append("turn_end", map[string]int{"turn": 1}); err != nil {
		t.Fatalf("Append 2: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Simulate crash mid-write on line 3 (no trailing newline, partial JSON).
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString(`{"seq":3,"ts":"2026-11-23T12:00:00Z","type":"tool_call","data":{"pa`); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()

	events, truncated, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile should recover from truncated tail, got err: %v", err)
	}
	if !truncated {
		t.Fatalf("expected truncated=true")
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 recovered events, got %d", len(events))
	}

	// Reopening and appending should trim the partial tail and continue cleanly at seq 3.
	j2, err := Open(path)
	if err != nil {
		t.Fatalf("Re-open after truncation: %v", err)
	}
	ev3, err := j2.Append("turn_start", map[string]int{"turn": 2})
	if err != nil {
		t.Fatalf("Append 3: %v", err)
	}
	if err := j2.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}
	if ev3.Seq != 3 {
		t.Fatalf("expected seq 3 after recovery, got %d", ev3.Seq)
	}

	events, truncated, err = ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after recovery append: %v", err)
	}
	if truncated {
		t.Fatalf("expected truncated=false after clean append")
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
}

func TestCorruptedMiddleLineErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	content := strings.Join([]string{
		`{"seq":1,"ts":"2026-11-23T12:00:00Z","type":"a"}`,
		`{"seq":2, BROKEN`,
		`{"seq":3,"ts":"2026-11-23T12:00:01Z","type":"c"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := ReadFile(path); err == nil {
		t.Fatalf("expected error for corrupted middle line, got nil")
	}
}
