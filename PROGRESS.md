# pu — Progress

Current milestone: **M2 — Loop, read, search** (M1 approved)

## M2 Checklist

### Pre-M2 fixes
- [x] `go mod tidy` (with `golang.org/x/sync` for `errgroup`)
- [x] `parseOpenAISSE`: return error if stream ends without `[DONE]` and without `finish_reason` (`TestStreamIncompleteWithoutDoneOrFinishReason`)
- [x] Deterministic fallback ID (`call_<turn>_<index>`) when a streamed tool call has empty `id` (`TestStreamToolCallEmptyIDGetsDeterministicID`)
- [x] Define cost as notional list-price cost alongside raw token counts; record 5-hour usage limit open question for M5

### M2 implementation
- [x] `internal/core/system.txt`: embedded Layer 1 system prompt (`go:embed`, zero session/repo variance)
- [x] `internal/core`: 4-layer KV-invariant prefix (Layer 1 static prompt, Layer 2 alphabetically sorted byte-identical tool schemas, Layer 3 session-start `AGENTS.md`/`PU.md`, Layer 4 history + Turn-0 fingerprint in first user turn)
- [x] `internal/core`: Turn-0 fingerprint (`git status --porcelain=v2`, branch, top-level tree summary, manifests, test runner)
- [x] `internal/edit`: `read` tool with 5-letter unique line anchors (even for identical lines) and file hash bound to revision
- [x] `internal/search`: `search` tool (`rg` with pure-Go `WalkDir` fallback; distinct `no_matches` vs `truncated` results)
- [x] `internal/core`: single-writer loop, parallel independent tool calls via `errgroup`, 64 KiB output spill to `.pu/sessions/<id>/out/`, budget limits (`--max-cost-usd`, `--max-turns`, `--timeout` -> `budget_exhausted`)
- [x] `cmd/pu run`: wire headless runner outputting JSON summary with raw tokens and notional list-price cost

### M2 Done-when gates
- [x] Scripted fake-provider session proves Layers 1–3 are byte-identical on every request (`TestFourLayerPrefixByteIdenticalAcrossTurns`)
- [x] Anchors stay unique in a file of 1,000 identical lines (`TestAnchorsUniqueIn1000IdenticalLines`)
- [x] Parallel reads come back in one tool-result turn (`TestFourLayerPrefixByteIdenticalAcrossTurns`)
- [x] Search no-match vs truncated is tested across `rg` and `WalkDir` fallback (`TestSearchNoMatchVsTruncated`)
- [x] Live two-turn tool-call smoke test confirms `deepseek-v4.1-flash` `reasoning_content` history behavior (`TestSmokeTwoTurnToolCallReasoningHistory`)
- [x] `go vet ./...`, `go test ./...`, and `CGO_ENABLED=0` release build pass (7.1 MiB static binary)

## M1 Checklist (Approved)

- [x] `go.mod`, `internal/catalog`, `internal/provider`, `internal/store`, `internal/exec`, `cmd/pu` (`doctor` + stubs)
- [x] All M1 done-when gates green, including live `TestSmokeOpenCodeGo` (`deepseek-v4.1-flash`, `reasoning_effort = "low"`, `in=38 out=16 total=54`)

## Decisions

- **OpenCode Go endpoint path & session header (verified live + against `GET https://opencode.ai/inference/go/v1/models` and docs)**: OpenAI chat-completions models (including the confirmed pilot model `deepseek-v4.1-flash`) use base URL `https://opencode.ai/inference/go/openai/v1` and `compatibility.reasoning_field = "reasoning_content"`. OpenCode Go requires a stable `x-opencode-session` header per conversation for routing and prompt caching; `provider.Request.SessionID` populates `x-opencode-session` when non-empty.
- **Confirmed pilot model**: `deepseek-v4.1-flash` on OpenCode Go (`openai-chat-compat`) with `reasoning_param = "reasoning_effort"` and `reasoning_effort = "low"`.
- **`reasoning_content` in assistant history on `deepseek-v4.1-flash` (verified live via `TestSmokeTwoTurnToolCallReasoningHistory`)**: **Accepted (optional)**. Turn 1 emitted `reasoning_content` alongside a tool call (`hasReasoning=true`, `in=314 cached=256 out=66`). Turn 2 succeeded both WITH `reasoning_content` preserved (`text="ORCHID-42"`, `in=403 cached=256 out=6`) and WITHOUT `reasoning_content` (`text="ORCHID-42"`, `in=378 cached=0 out=7`). Preserving `ReasoningPart` in assistant history is accepted cleanly and retains KV cache hits when reusing `x-opencode-session`.
- **Notional list-price cost vs subscription billing**: Because OpenCode Go is a flat-rate subscription, local USD cost computed from `models.toml` prices is defined as **notional list-price cost** (used for apples-to-apples efficiency comparison and `--max-cost-usd` budgets). Every run report and journal usage event records **raw token counts** (`input_tokens`, `cached_input_tokens`, `output_tokens`) alongside notional list-price cost.
- **Deterministic tool-call fallback ID**: If a streamed tool call arrives with an empty `id`, `internal/provider` assigns `call_<turn>_<index>` so history replay and tool results remain deterministic.
- **Anchored read & revision table location**: Placed 5-letter line anchor generation and per-file SHA-256 revision tracking in `internal/edit` so M2 `read` and M3 `hashline` `patch` share the exact same anchor algorithm and revision state.
- **Layer 3 project rules order**: At session start, `AGENTS.md` and `PU.md` (if present in repo root) are read once in fixed order (`AGENTS.md`, then `PU.md`) and pinned into Layer 3 without mid-session mutation.
- **Output spill threshold**: Tool outputs exceeding 64 KiB (65,536 bytes) are written to `.pu/sessions/<id>/out/<call_id>.txt`; the model receives an excerpt (first 8 KiB + last 4 KiB) with byte/line count and file reference.
- **`models.toml` schema**: Each `[models."<name>"]` table supports `driver`, `endpoint`, `auth` (`"env:VAR"` or `"none"`), `model`, `context_window`, `reasoning_param`, `reasoning_effort`, and `input_price_per_m` / `cached_input_price_per_m` / `output_price_per_m` (notional USD per 1M tokens).
- **Usage cached-token fields**: Parser accepts both OpenAI `usage.prompt_tokens_details.cached_tokens` and DeepSeek-style `usage.prompt_cache_hit_tokens`.

## Open questions

- **M5 pilot pacing vs OpenCode Go 5-hour usage limit**: OpenCode Go enforces a rolling 5-hour allowance (20% of monthly allowance) and weekly limit (50%). For the 30-task pilot in M5 (60 runs total across `pu` and `jcode`), the harness will need to handle or pace around `429` / rate-limit responses (or check `GET /inference/go/v1/usage`) so neither tool is penalized by mid-pilot throttling.
