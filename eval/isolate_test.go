package eval

import (
	"os"
	"strings"
	"testing"
)

func TestIsolatedCheckoutHidesFutureCommitsAndAcceptanceSeesThem(t *testing.T) {
	src := t.TempDir()
	run := func(args ...string) string {
		out, err := gitIn(src, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	run("init", "-q")
	_ = os.WriteFile(src+"/a.txt", []byte("1"), 0o644)
	run("add", ".")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base")
	base := run("rev-parse", "HEAD")
	_ = os.WriteFile(src+"/a.txt", []byte("2"), 0o644)
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qam", "THE FIX")
	fix := run("rev-parse", "HEAD")

	if err := isolatedCheckout(src, base, t.TempDir()); err != nil {
		t.Fatalf("isolated: %v", err)
	}
	dir, cleanup, err := createWorktree(src, base)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if out, _ := gitIn(dir, "log", "--all", "--oneline"); strings.Contains(out, "THE FIX") {
		t.Fatalf("future commit visible: %s", out)
	}
	if _, err := gitIn(dir, "cat-file", "-e", fix); err == nil {
		t.Fatal("fix object reachable before acceptance")
	}
	fetchForAcceptance(src, dir)
	if _, err := gitIn(dir, "checkout", fix[:8], "--", "a.txt"); err != nil {
		t.Fatalf("acceptance can't reach fix: %v", err)
	}
}

func TestScanContamination(t *testing.T) {
	later := map[string]bool{"abcdef1": true}
	if s, _ := ScanContamination("ok  \tpkg\t0.1s", later); s != ContamClean {
		t.Fatal(s)
	}
	if s, _ := ScanContamination("abcdef1 fix the bug\n", later); s != ContamHistory {
		t.Fatal(s)
	}
	if s, _ := ScanContamination("abcdef1234\tHEAD\n", later); s != ContamNetwork {
		t.Fatal(s)
	}
	if s, _ := ScanContamination("Cloning into 'x'...", later); s != ContamNetwork {
		t.Fatal(s)
	}
	if s, _ := ScanContamination("GOPROXY=https://proxy.golang.org,direct", later); s != ContamClean {
		t.Fatal("GOPROXY env value is not a fetch:", s)
	}
}
