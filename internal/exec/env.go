package exec

import (
	"fmt"
	"strings"
)

var allowedEnvKeys = map[string]bool{
	"PATH":             true,
	"HOME":             true,
	"USER":             true,
	"LOGNAME":          true,
	"SHELL":            true,
	"LANG":             true,
	"LC_ALL":           true,
	"LC_CTYPE":         true,
	"TERM":             true,
	"TMPDIR":           true,
	"TEMP":             true,
	"TMP":              true,
	"TZ":               true,
	"PWD":              true,
	"SHLVL":            true,
	"XDG_CACHE_HOME":   true,
	"XDG_CONFIG_HOME":  true,
	"XDG_DATA_HOME":    true,
	"XDG_RUNTIME_DIR":  true,
	"GOPATH":           true,
	"GOMODCACHE":       true,
	"GOCACHE":          true,
	"GOROOT":           true,
	"GOTOOLCHAIN":      true,
	"GOPROXY":          true,
	"GOSUMDB":          true,
	"CGO_ENABLED":      true,
	"NODE_PATH":        true,
	"NPM_CONFIG_CACHE": true,
	"YARN_CACHE_FOLDER": true,
	"PNPM_HOME":        true,
	"VIRTUAL_ENV":      true,
	"PYTHONPATH":       true,
	"PYTHONUNBUFFERED": true,
	"PIP_CACHE_DIR":    true,
	"UV_CACHE_DIR":     true,
	"CARGO_HOME":       true,
	"RUSTUP_HOME":      true,
}

var deniedEnvSuffixes = []string{
	"_API_KEY",
	"_TOKEN",
	"_SECRET",
	"_PASSWORD",
	"_PASSWD",
	"_CREDENTIAL",
	"_CREDENTIALS",
	"_PRIVATE_KEY",
	"_ACCESS_KEY",
}

var deniedProviderPrefixes = []string{
	"OPENAI_",
	"ANTHROPIC_",
	"GEMINI_",
	"GOOGLE_API",
	"OPENCODE_",
	"OPENROUTER_",
	"DEEPSEEK_",
	"XAI_",
	"GROQ_",
	"MISTRAL_",
	"COHERE_",
	"TOGETHER_",
	"FIREWORKS_",
	"AWS_",
	"AZURE_",
	"GCP_",
	"GH_",
	"GITHUB_",
	"GITLAB_",
	"HF_",
	"HUGGINGFACE_",
}

// ScrubEnv filters a process environment list using a strict allowlist plus
// explicit secret/provider denylist rules so child processes never inherit
// API keys, tokens, or secrets.
func ScrubEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		if !IsAllowedEnvKey(key) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// IsAllowedEnvKey reports whether key may be passed to a child process.
func IsAllowedEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, suf := range deniedEnvSuffixes {
		if strings.HasSuffix(upper, suf) {
			return false
		}
	}
	for _, pre := range deniedProviderPrefixes {
		if strings.HasPrefix(upper, pre) {
			return false
		}
	}
	return allowedEnvKeys[key]
}

// SelfTestEnvScrub verifies that ScrubEnv strips synthetic API keys, tokens,
// secrets, and provider-prefixed variables while preserving allowlisted ones.
func SelfTestEnvScrub() error {
	sample := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/test",
		"GOCACHE=/tmp/gocache",
		"OPENCODE_API_KEY=opencode-secret-value",
		"OPENAI_API_KEY=sk-secret",
		"ANTHROPIC_BASE_URL=https://example.com",
		"MY_TOKEN=tok-123",
		"DB_SECRET=sec-456",
		"RANDOM_VAR=drop-me",
	}
	scrubbed := ScrubEnv(sample)
	got := make(map[string]string, len(scrubbed))
	for _, kv := range scrubbed {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for _, mustKeep := range []string{"PATH", "HOME", "GOCACHE"} {
		if _, ok := got[mustKeep]; !ok {
			return fmt.Errorf("env-scrub self-test failed: allowlisted var %s was dropped", mustKeep)
		}
	}
	for _, mustDrop := range []string{
		"OPENCODE_API_KEY",
		"OPENAI_API_KEY",
		"ANTHROPIC_BASE_URL",
		"MY_TOKEN",
		"DB_SECRET",
		"RANDOM_VAR",
	} {
		if _, ok := got[mustDrop]; ok {
			return fmt.Errorf("env-scrub self-test failed: sensitive/unlisted var %s leaked", mustDrop)
		}
	}
	return nil
}
