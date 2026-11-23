package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event is one append-only record in events.jsonl.
type Event struct {
	Seq  uint64          `json:"seq"`
	Time time.Time       `json:"ts"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Journal manages an append-only events.jsonl file.
type Journal struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	nextSeq uint64
}

// SessionPath returns <repoRoot>/.pu/sessions/<sessionID>/events.jsonl.
func SessionPath(repoRoot, sessionID string) string {
	return filepath.Join(repoRoot, ".pu", "sessions", sessionID, "events.jsonl")
}

// Open opens (or creates) an append-only events.jsonl journal.
// If an existing file ends with a truncated final line, the trailing partial
// bytes are trimmed so subsequent appends stay well-formed.
func Open(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create journal dir: %w", err)
	}

	var nextSeq uint64 = 1
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		events, validOffset, truncatedTail, err := readFileWithOffset(path)
		if err != nil {
			return nil, fmt.Errorf("read existing journal: %w", err)
		}
		if len(events) > 0 {
			nextSeq = events[len(events)-1].Seq + 1
		}
		if truncatedTail {
			if err := os.Truncate(path, validOffset); err != nil {
				return nil, fmt.Errorf("truncate partial tail: %w", err)
			}
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}

	return &Journal{
		path:    path,
		file:    f,
		nextSeq: nextSeq,
	}, nil
}

// Append writes one event line to the journal.
func (j *Journal) Append(eventType string, payload any) (Event, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.file == nil {
		return Event{}, errors.New("journal is closed")
	}

	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return Event{}, fmt.Errorf("marshal event %q payload: %w", eventType, err)
		}
		raw = b
	}

	ev := Event{
		Seq:  j.nextSeq,
		Time: time.Now().UTC(),
		Type: eventType,
		Data: raw,
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return Event{}, fmt.Errorf("marshal event: %w", err)
	}
	line = append(line, '\n')

	if _, err := j.file.Write(line); err != nil {
		return Event{}, fmt.Errorf("write event: %w", err)
	}
	j.nextSeq++
	return ev, nil
}

// Path returns the filesystem path of the journal.
func (j *Journal) Path() string {
	return j.path
}

// Close closes the underlying file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return nil
	}
	err := j.file.Close()
	j.file = nil
	return err
}

// ReadFile reads all valid events from an events.jsonl file.
// If the final line is truncated or malformed JSON, it is safely skipped and
// truncatedTail is returned as true. Malformed non-final lines return an error.
func ReadFile(path string) (events []Event, truncatedTail bool, err error) {
	events, _, truncatedTail, err = readFileWithOffset(path)
	return events, truncatedTail, err
}

func readFileWithOffset(path string) (events []Event, validOffset int64, truncatedTail bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64*1024)
	var offset int64
	lineNum := 0

	for {
		line, readErr := r.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, 0, false, readErr
		}
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		lineNum++
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			offset += int64(len(line))
			validOffset = offset
			if errors.Is(readErr, io.EOF) {
				break
			}
			continue
		}

		var ev Event
		if uErr := json.Unmarshal(trimmed, &ev); uErr != nil {
			// Check whether any non-whitespace bytes remain after this line.
			isLast := errors.Is(readErr, io.EOF)
			if !isLast {
				rem, _ := io.ReadAll(r)
				if len(bytes.TrimSpace(rem)) == 0 {
					isLast = true
				}
			}
			if isLast {
				return events, validOffset, true, nil
			}
			return nil, 0, false, fmt.Errorf("line %d: %w", lineNum, uErr)
		}

		events = append(events, ev)
		offset += int64(len(line))
		validOffset = offset
		if errors.Is(readErr, io.EOF) {
			break
		}
	}

	return events, validOffset, false, nil
}
