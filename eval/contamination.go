package eval

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Contamination labels for a run.
const (
	ContamClean   = "clean"
	ContamHistory = "history_leak"
	ContamNetwork = "network_leak"
)

var (
	netLeakRe = regexp.MustCompile(`(From https?://|Cloning into|remote: (Enumerating|Counting)|` +
		`go: downloading \S+ v|go: (finding|upgraded)|` +
		`github\.com/[^\s/"]+/[^\s/"]+/(commit|pull|compare)/|raw\.githubusercontent|codeload\.github|get https://proxy\.golang\.org/|` +
		`HTTP/[12](\.\d)? 200|% Total +% Received)`)
	shaRe = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// laterCommitPrefixes returns 7-char prefixes of commits in repoDir that are not
// ancestors of start (i.e. upstream history the agent must not see).
func laterCommitPrefixes(repoDir, start string) map[string]bool {
	out, err := osexec.Command("git", "-C", repoDir, "rev-list", "--all", "^"+start).Output()
	m := map[string]bool{}
	if err != nil {
		return m
	}
	for _, s := range strings.Fields(string(out)) {
		if len(s) >= 7 {
			m[s[:7]] = true
		}
	}
	return m
}

// ScanContamination classifies a run transcript. Network evidence wins over history.
func ScanContamination(transcript string, later map[string]bool) (string, string) {
	if m := netLeakRe.FindString(transcript); m != "" {
		return ContamNetwork, m
	}
	for _, s := range shaRe.FindAllString(transcript, -1) {
		if later[s[:7]] {
			if regexp.MustCompile(regexp.QuoteMeta(s) + `\s+(HEAD|refs/)`).MatchString(transcript) {
				return ContamNetwork, s + " (ls-remote)"
			}
			return ContamHistory, s
		}
	}
	return ContamClean, ""
}

// collectTranscript concatenates journal/log text under dirs (pu .pu/sessions,
// jcode isolated home). Bounded to keep memory sane.
func collectTranscript(dirs ...string) string {
	var sb strings.Builder
	const max = 64 << 20
	for _, d := range dirs {
		_ = filepath.Walk(d, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || sb.Len() > max {
				return nil
			}
			ext := filepath.Ext(p)
			if ext != ".jsonl" && ext != ".json" && ext != ".log" {
				return nil
			}
			if b, rErr := os.ReadFile(p); rErr == nil {
				sb.Write(b)
				sb.WriteByte('\n')
			}
			return nil
		})
	}
	// JSON-escaped tabs/newlines in journals: unescape the common ones so line regexes work.
	r := strings.NewReplacer(`\t`, "\t", `\n`, "\n")
	return r.Replace(sb.String())
}
