package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorValidCatalogAndMaskedKey(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.toml")
	content := `
default = "deepseek"

[models.deepseek]
driver = "openai-chat-compat"
model = "deepseek-v4.1-flash"
auth = "env:OPENCODE_API_KEY"
context_window = 1000000
input_price_per_m = 0.15
cached_input_price_per_m = 0.003
output_price_per_m = 0.60
`
	if err := os.WriteFile(modelsPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	secret := "oc-super-secret-token-9999"
	var out bytes.Buffer
	code := execDoctor(doctorDeps{
		modelsPath: modelsPath,
		getenv: func(k string) string {
			if k == "OPENCODE_API_KEY" {
				return secret
			}
			return ""
		},
		lookPath: func(name string) (string, error) {
			return "/usr/bin/rg", nil
		},
	}, &out)

	if code != 0 {
		t.Fatalf("doctor returned %d, want 0:\n%s", code, out.String())
	}
	got := out.String()
	if strings.Contains(got, secret) {
		t.Fatalf("doctor output leaked raw secret key:\n%s", got)
	}
	for _, want := range []string{
		"[ok]   go:",
		"[ok]   models.toml: valid",
		"oc-s...9999",
		"[ok]   rg: /usr/bin/rg",
		"[ok]   env-scrub: self-test passed",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorInvalidCatalogFails(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.toml")
	if err := os.WriteFile(modelsPath, []byte("not valid toml = = ="), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var out bytes.Buffer
	code := execDoctor(doctorDeps{
		modelsPath: modelsPath,
		getenv:     func(string) string { return "" },
		lookPath:   func(string) (string, error) { return "/usr/bin/rg", nil },
	}, &out)
	if code == 0 {
		t.Fatalf("expected non-zero exit code for invalid models.toml:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[fail] models.toml") {
		t.Fatalf("expected fail line in output:\n%s", out.String())
	}
}
