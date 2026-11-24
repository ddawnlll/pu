package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchNoMatchVsTruncated(t *testing.T) {
	dir := t.TempDir()

	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&sb, "line %d: needle_token value=%d\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for _, forceFallback := range []bool{false, true} {
		mode := "rg"
		if forceFallback {
			mode = "go"
		}
		t.Run(mode, func(t *testing.T) {
			// 1. No matches
			resNo, err := Search(context.Background(), Options{
				Root:          dir,
				Query:         "definitely_absent_xyz",
				MaxResults:    5,
				ForceFallback: forceFallback,
			})
			if err != nil {
				t.Fatalf("Search no-match: %v", err)
			}
			if resNo.Status != StatusNoMatches || len(resNo.Matches) != 0 {
				t.Fatalf("want StatusNoMatches with 0 matches, got %+v", resNo)
			}

			// 2. Truncated
			resTrunc, err := Search(context.Background(), Options{
				Root:          dir,
				Query:         "needle_token",
				MaxResults:    5,
				ForceFallback: forceFallback,
			})
			if err != nil {
				t.Fatalf("Search truncated: %v", err)
			}
			if resTrunc.Status != StatusTruncated || len(resTrunc.Matches) != 5 {
				t.Fatalf("want StatusTruncated with 5 matches, got %+v", resTrunc)
			}

			// 3. Exact OK (all 20 fit under limit 50)
			resOK, err := Search(context.Background(), Options{
				Root:          dir,
				Query:         "needle_token",
				MaxResults:    50,
				ForceFallback: forceFallback,
			})
			if err != nil {
				t.Fatalf("Search ok: %v", err)
			}
			if resOK.Status != StatusOK || len(resOK.Matches) != 20 {
				t.Fatalf("want StatusOK with 20 matches, got %+v", resOK)
			}
		})
	}
}
