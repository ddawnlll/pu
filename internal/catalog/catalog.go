package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	DriverOpenAIChatCompat = "openai-chat-compat"
	DriverOpenAIResponses  = "openai-responses"
	DriverAnthropic        = "anthropic-messages"
	DriverGoogleGemini     = "google-gemini"

	// Verified against GET https://opencode.ai/inference/go/v1/models
	DefaultEndpointOpenCodeGo = "https://opencode.ai/inference/go/openai/v1"
)

// Catalog holds the models loaded from models.toml.
type Catalog struct {
	Default string           `toml:"default"`
	Models  map[string]Model `toml:"models"`
}

// Model describes one model entry in models.toml.
type Model struct {
	Key                  string   `toml:"-"`
	ID                   string   `toml:"model"`
	Driver               string   `toml:"driver"`
	Endpoint             string   `toml:"endpoint"`
	Auth                 string   `toml:"auth"`
	ContextWindow        int      `toml:"context_window"`
	ReasoningParam       string   `toml:"reasoning_param"`
	ReasoningEffort      string   `toml:"reasoning_effort"`
	InputPricePerM       float64  `toml:"input_price_per_m"`
	CachedInputPricePerM *float64 `toml:"cached_input_price_per_m"`
	OutputPricePerM      float64  `toml:"output_price_per_m"`
}

// DefaultPath returns ~/.pu/models.toml.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".pu", "models.toml"), nil
}

// Load reads and validates a models.toml file from disk.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes and validates models.toml bytes.
func Parse(data []byte) (*Catalog, error) {
	var c Catalog
	md, err := toml.Decode(string(data), &c)
	if err != nil {
		return nil, fmt.Errorf("decode models.toml: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("models.toml has unknown keys: %s", strings.Join(keys, ", "))
	}
	if len(c.Models) == 0 {
		return nil, fmt.Errorf("models.toml defines no [models.<name>] entries")
	}

	for key, m := range c.Models {
		m.Key = key
		if m.ID == "" {
			m.ID = key
		}
		if err := validateModel(&m); err != nil {
			return nil, fmt.Errorf("model %q: %w", key, err)
		}
		c.Models[key] = m
	}

	if c.Default != "" {
		if _, ok := c.Models[c.Default]; !ok {
			return nil, fmt.Errorf("default model %q not found in [models]", c.Default)
		}
	}

	return &c, nil
}

func validateModel(m *Model) error {
	switch m.Driver {
	case DriverOpenAIChatCompat:
		if m.Endpoint == "" {
			m.Endpoint = DefaultEndpointOpenCodeGo
		}
	case DriverOpenAIResponses, DriverAnthropic, DriverGoogleGemini:
	case "":
		return fmt.Errorf("driver is required")
	default:
		return fmt.Errorf("unsupported driver %q", m.Driver)
	}

	if m.Auth != "" && m.Auth != "none" {
		if !strings.HasPrefix(m.Auth, "env:") || len(strings.TrimSpace(strings.TrimPrefix(m.Auth, "env:"))) == 0 {
			return fmt.Errorf("invalid auth %q (expected \"env:VAR\" or \"none\")", m.Auth)
		}
	}
	if m.ContextWindow < 0 {
		return fmt.Errorf("context_window must be >= 0")
	}
	if m.InputPricePerM < 0 || m.OutputPricePerM < 0 {
		return fmt.Errorf("prices must be >= 0")
	}
	if m.CachedInputPricePerM != nil && *m.CachedInputPricePerM < 0 {
		return fmt.Errorf("cached_input_price_per_m must be >= 0")
	}
	return nil
}

// Model looks up a model by key. If name is empty, it returns the default model
// (or the sole configured model when only one exists).
func (c *Catalog) Model(name string) (Model, error) {
	if name == "" {
		if c.Default != "" {
			name = c.Default
		} else if len(c.Models) == 1 {
			for k := range c.Models {
				name = k
			}
		} else {
			return Model{}, fmt.Errorf("no model specified and no default set in models.toml")
		}
	}
	m, ok := c.Models[name]
	if !ok {
		return Model{}, fmt.Errorf("model %q not found in catalog", name)
	}
	return m, nil
}

// Names returns all configured model keys sorted alphabetically.
func (c *Catalog) Names() []string {
	names := make([]string, 0, len(c.Models))
	for k := range c.Models {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// ResolveAPIKey resolves the API key for the model using only the configured
// environment variable. It never reads credential files of other tools.
func (m Model) ResolveAPIKey(getenv func(string) string) (key string, envVar string, required bool, err error) {
	if m.Auth == "" || m.Auth == "none" {
		return "", "", false, nil
	}
	if !strings.HasPrefix(m.Auth, "env:") {
		return "", "", true, fmt.Errorf("unsupported auth %q", m.Auth)
	}
	envVar = strings.TrimSpace(strings.TrimPrefix(m.Auth, "env:"))
	if envVar == "" {
		return "", "", true, fmt.Errorf("empty env var in auth %q", m.Auth)
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv(envVar), envVar, true, nil
}

// ComputeCostUSD calculates local cost in USD from token usage.
// cachedInputTokens is treated as a subset of inputTokens (matching OpenAI/DeepSeek usage).
func (m Model) ComputeCostUSD(inputTokens, cachedInputTokens, outputTokens int) float64 {
	if cachedInputTokens < 0 {
		cachedInputTokens = 0
	}
	if inputTokens < 0 {
		inputTokens = 0
	}
	if outputTokens < 0 {
		outputTokens = 0
	}
	if cachedInputTokens > inputTokens {
		cachedInputTokens = inputTokens
	}
	uncached := inputTokens - cachedInputTokens
	cachedPrice := m.InputPricePerM
	if m.CachedInputPricePerM != nil {
		cachedPrice = *m.CachedInputPricePerM
	}
	return (float64(uncached)*m.InputPricePerM +
		float64(cachedInputTokens)*cachedPrice +
		float64(outputTokens)*m.OutputPricePerM) / 1_000_000.0
}

// MaskSecret obscures a secret key for display (e.g. in pu doctor).
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "..." + s[len(s)-4:]
}
