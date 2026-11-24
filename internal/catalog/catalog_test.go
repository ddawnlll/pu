package catalog

import (
	"math"
	"testing"
)

func TestParseAndLookup(t *testing.T) {
	raw := `
default = "deepseek"

[models.deepseek]
driver = "openai-chat-compat"
model = "deepseek-v4.1-flash"
auth = "env:OPENCODE_API_KEY"
context_window = 1000000
reasoning_param = "reasoning_effort"
reasoning_effort = "high"
input_price_per_m = 0.15
cached_input_price_per_m = 0.003
output_price_per_m = 0.60
`
	cat, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	m, err := cat.Model("")
	if err != nil {
		t.Fatalf("Model default failed: %v", err)
	}
	if m.Key != "deepseek" || m.ID != "deepseek-v4.1-flash" {
		t.Fatalf("unexpected model key/id: %+v", m)
	}
	if m.Endpoint != DefaultEndpointOpenCodeGo {
		t.Fatalf("expected default OpenCode Go endpoint %q, got %q", DefaultEndpointOpenCodeGo, m.Endpoint)
	}

	key, envVar, required, err := m.ResolveAPIKey(func(k string) string {
		if k == "OPENCODE_API_KEY" {
			return "oc-test-1234567890"
		}
		return ""
	})
	if err != nil || !required || envVar != "OPENCODE_API_KEY" || key != "oc-test-1234567890" {
		t.Fatalf("unexpected ResolveAPIKey: key=%q env=%q req=%v err=%v", key, envVar, required, err)
	}
	if got := MaskSecret(key); got != "oc-t...7890" {
		t.Fatalf("MaskSecret = %q, want oc-t...7890", got)
	}
	if got := MaskSecret("short"); got != "****" {
		t.Fatalf("MaskSecret(short) = %q, want ****", got)
	}
}

func TestComputeCostUSD(t *testing.T) {
	cached := 0.003
	m := Model{
		InputPricePerM:       0.15,
		CachedInputPricePerM: &cached,
		OutputPricePerM:      0.60,
	}
	// 1,000,000 total input tokens of which 800,000 are cached -> 200k uncached * 0.15 + 800k * 0.003 + 500k * 0.60
	// = 0.03 + 0.0024 + 0.30 = 0.3324
	got := m.ComputeCostUSD(1_000_000, 800_000, 500_000)
	want := 0.3324
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("ComputeCostUSD = %.9f, want %.9f", got, want)
	}
}

func TestParseValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		toml string
	}{
		{"empty", ``},
		{"unknown key", "[models.a]\ndriver = \"openai-chat-compat\"\nbogus = 1\n"},
		{"bad driver", "[models.a]\ndriver = \"nope\"\n"},
		{"bad auth", "[models.a]\ndriver = \"openai-chat-compat\"\nauth = \"file:/etc/passwd\"\n"},
		{"missing default", "default = \"missing\"\n[models.a]\ndriver = \"openai-chat-compat\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.toml)); err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}
