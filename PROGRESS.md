# pu — Progress

Current milestone: **M1 — Skeleton and driver**

## M1 Checklist

- [x] `go.mod` (`go 1.27`, `github.com/ddawnlll/pu`, allowed dependencies only)
- [x] `internal/catalog`: load `models.toml` (driver, endpoint, auth, model id, context_window, prices, reasoning_param) + local cost calculation
- [x] `internal/provider`: typed `Item` / `ContentPart` union (spec §07-B: Text, Image, ToolCall, ToolResult, Reasoning; Video, Audio, FileRef declared unused)
- [x] `internal/provider`: streaming SSE client for `openai-chat-compat` (tool calls, reasoning deltas, usage with cached tokens, `User-Agent: pu/<version>`)
- [x] `internal/store`: append-only `.pu/sessions/<id>/events.jsonl` with safe truncated last-line recovery
- [x] `internal/exec`: allowlist env scrubbing (`ScrubEnv`) for `pu doctor` self-test and M4 reuse
- [x] `cmd/pu/main.go`: subcommands `run`, `diff` (stub), `undo` (stub), `doctor` (Go version, `models.toml` validity, masked API key, `rg` present, env-scrub self-test)

### M1 Done-when gates

- [x] Tests cover UTF-8 split across SSE chunks (`TestStreamUTF8SplitAcrossChunks`)
- [x] Tests cover tool-call argument deltas split across chunks (`TestStreamToolCallArgsSplitAcrossChunks`)
- [x] Tests cover usage parsing (including cached tokens) (`TestStreamUsageAndCachedTokens`)
- [x] Tests cover truncated JSONL recovery (`TestTruncatedLastLineRecovery`)
- [x] `go vet ./...` and `go test ./...` pass
- [x] Release build succeeds: `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pu ./cmd/pu` (2.2 MiB static binary)
- [x] Opt-in smoke test (`TestSmokeOpenCodeGo`) implemented and skips cleanly when `OPENCODE_API_KEY` is unset (ready to pass when set)

## Decisions

- **OpenCode Go endpoint path (verified against `GET https://opencode.ai/inference/go/v1/models`)**: OpenAI chat-completions models (e.g. `deepseek-v4.1-flash`, `glm-5.3`, `kimi-k2.7-code`) advertise `api.chat-completions.base_url = "https://opencode.ai/inference/go/openai/v1"` and `compatibility.reasoning_field = "reasoning_content"`. The `openai-chat-compat` driver appends `/chat/completions` to the configured `endpoint` (defaulting to `https://opencode.ai/inference/go/openai/v1` when omitted) and reads both `delta.reasoning_content` and `delta.reasoning`.
- **`models.toml` schema**: Each `[models."<name>"]` table supports `driver`, `endpoint`, `auth` (`"env:VAR"` or `"none"`), `model` (optional wire model ID override, defaults to the table key), `context_window`, `reasoning_param`, `reasoning_effort`, and `input_price_per_m` / `cached_input_price_per_m` / `output_price_per_m` (USD per 1M tokens). Cached input tokens are billed at `cached_input_price_per_m` (if set, else `input_price_per_m`) and subtracted from total prompt tokens so input tokens are not double-counted.
- **Usage cached-token fields**: Parser accepts both OpenAI `usage.prompt_tokens_details.cached_tokens` and DeepSeek-style `usage.prompt_cache_hit_tokens`.
- **Env scrub location**: Placed `ScrubEnv` in `internal/exec/env.go` (matching spec §02 package layout) so `pu doctor` in M1 and `exec` in M4 share the exact same allowlist and secret-dropping logic.
- **Store format**: `events.jsonl` stores one JSON object per line with `seq`, `ts` (RFC3339Nano UTC), `type`, and `data` (`json.RawMessage`). On replay, a truncated or malformed final line is safely ignored (and reported via a counter) while malformed non-final lines return an error.

## Open questions

- Which exact OpenCode Go model ID will be used for the 30-task pilot (e.g. `deepseek-v4.1-flash`, `glm-5.3`, `kimi-k2.7-code`)? Note: `gpt-6-luna` / `grok-4.7` advertise `recommended_api = "responses"`, while `claude-haiku-5-5` / `minimax-m3` / `qwen3.8-max` advertise `recommended_api = "messages"`, whereas `deepseek-v4.1-flash`, `deepseek-v4-pro`, `glm-5.3`, `glm-5.3-flash`, `kimi-k2.7-code`, `mimo-v2.6-flash`, `minimax-m3.1-flash` all use `chat-completions`.
- `OPENCODE_API_KEY` is not set in this environment, so the opt-in smoke test skips locally; run `OPENCODE_API_KEY=... go test -v ./internal/provider -run TestSmokeOpenCodeGo` once a key is provided.
