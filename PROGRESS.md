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
- [x] Opt-in smoke test (`TestSmokeOpenCodeGo`) passed live against OpenCode Go (`deepseek-v4.1-flash`, `reasoning_effort = "low"`, text=`"pong"`, usage `in=38 out=16 total=54`)

## Decisions

- **OpenCode Go endpoint path & session header (verified live + against `GET https://opencode.ai/inference/go/v1/models` and docs)**: OpenAI chat-completions models (including the confirmed pilot model `deepseek-v4.1-flash`) use base URL `https://opencode.ai/inference/go/openai/v1` and `compatibility.reasoning_field = "reasoning_content"`. OpenCode Go requires a stable `x-opencode-session` header per conversation for routing and prompt caching; `provider.Request.SessionID` populates `x-opencode-session` when non-empty.
- **Confirmed pilot model**: `deepseek-v4.1-flash` on OpenCode Go (`openai-chat-compat`) with `reasoning_param = "reasoning_effort"` and `reasoning_effort = "low"`.
- **`models.toml` schema**: Each `[models."<name>"]` table supports `driver`, `endpoint`, `auth` (`"env:VAR"` or `"none"`), `model` (optional wire model ID override, defaults to the table key), `context_window`, `reasoning_param`, `reasoning_effort`, and `input_price_per_m` / `cached_input_price_per_m` / `output_price_per_m` (USD per 1M tokens). Cached input tokens are billed at `cached_input_price_per_m` (if set, else `input_price_per_m`) and subtracted from total prompt tokens so input tokens are not double-counted.
- **Usage cached-token fields**: Parser accepts both OpenAI `usage.prompt_tokens_details.cached_tokens` and DeepSeek-style `usage.prompt_cache_hit_tokens`.
- **Env scrub location**: Placed `ScrubEnv` in `internal/exec/env.go` (matching spec §02 package layout) so `pu doctor` in M1 and `exec` in M4 share the exact same allowlist and secret-dropping logic.
- **Store format**: `events.jsonl` stores one JSON object per line with `seq`, `ts` (RFC3339Nano UTC), `type`, and `data` (`json.RawMessage`). On replay, a truncated or malformed final line is safely ignored (and reported via a counter) while malformed non-final lines return an error.

## Open questions

- None for M1. Ready for review and M2 go-ahead.
