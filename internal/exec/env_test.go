package exec

import "testing"

func TestSelfTestEnvScrub(t *testing.T) {
	if err := SelfTestEnvScrub(); err != nil {
		t.Fatalf("SelfTestEnvScrub failed: %v", err)
	}
}

func TestScrubEnvDropsSecretsAndProviderPrefixes(t *testing.T) {
	in := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"HOME=/home/dev",
		"GOPATH=/home/dev/go",
		"OPENCODE_API_KEY=secret1",
		"opencode_custom=secret2",
		"FOO_TOKEN=tok",
		"BAR_SECRET=sec",
		"AWS_ACCESS_KEY_ID=akia",
		"GITHUB_TOKEN=ghp_xxx",
		"UNLISTED_VAR=hello",
		"MALFORMED_NO_EQUALS",
	}
	out := ScrubEnv(in)
	if len(out) != 3 {
		t.Fatalf("expected 3 kept vars, got %d: %v", len(out), out)
	}
}
