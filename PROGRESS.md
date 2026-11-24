# pu — Progress

Current milestone: **P0 Complete (M1–M5 merged to `main`)** — ready for the 30-task pilot

## M3 Checklist — Edit and undo (Merged)

- [x] `internal/edit`: `patch` tool supporting `hashline` (default), `search_replace` (exact 1 match), `full_rewrite`, `create`, `delete`
- [x] `internal/edit`: strict revision lock (stale anchor or file hash rejected with zero fuzzy matching, carrying fresh anchors for the affected range); success returns fresh-anchored diff (≤50 KiB)
- [x] `internal/edit`: session checkpointing under `.pu/checkpoints/<session>/` + `manifest.json` before first write to each file
- [x] `internal/edit`: `Undo` (restores checkpointed files, removes created files, leaves untouched dirty files alone) and `Diff` (session change summary)
- [x] `internal/core`: register `patch` in Layer 2 (alphabetical: `patch`, `read`, `search`) and wire session checkpointer
- [x] `cmd/pu`: implement `pu diff` and `pu undo` subcommands

### M3 Done-when gates
- [x] Stale anchors rejected with fresh anchors returned (`TestHashlineStaleAnchorRejectedWithFreshAnchors`)
- [x] Edits on identical lines hit the intended line (`TestHashlineEditsIntendedIdenticalLine`)
- [x] `search_replace` with 0 or 2+ matches rejected (`TestSearchReplaceRejectsZeroOrMultipleMatches`)
- [x] Dirty git tree undo restores user's uncommitted version of edited file and leaves untouched dirty files alone (`TestDirtyGitTreeUndoRestoresUncommittedAndLeavesUntouchedAlone`)
- [x] Create/delete round-trip through undo (`TestCreateDeleteRoundTripThroughUndo`)
- [x] `go vet ./...`, `go test ./...`, and `CGO_ENABLED=0` release build pass (7.2 MiB)

## M4 Checklist — Exec, verification, stopping (Merged)

- [x] `internal/exec`: `Run` with pipes (no PTY in P0), `Setpgid`, timeout, SIGINT -> SIGTERM to `-pgid` -> 300ms -> SIGKILL, allowlist scrubbed env, 64 KiB output cap
- [x] `internal/exec` + `internal/edit`: post-`exec` `stat` (mtime + size) on tracked files, hash changed files, bump revision
- [x] `internal/plan`: in-memory plan mirrored to `.pu/plan.json` (spec §05 shape), evidence records (command, exit code, revision, marked `stale` when any covered file's revision changes)
- [x] `internal/core`: terminal statuses (`completed`, `blocked`, `budget_exhausted`, `interrupted`, `failed`), `completed` requires current evidence for every plan step, up to 2 open-step continuations then `failed`
- [x] `internal/core`: external-content gate (`--untrusted-repo` and reads outside repo root -> next `exec` returns `blocked` in headless unless `--trust-untrusted`; normal local flow never asks)
- [x] `internal/core`: 2-stage KV-friendly context ceiling (≥75% trim superseded reads & stale test output to short refs; ≥85% clean state handoff to fresh prefix; `events.jsonl` keeps everything)
- [x] `internal/core` + `cmd/pu run`: register `exec` and `plan` tools (alphabetical Layer 2), final report JSON (status, turns, raw tokens, notional cost, wall time, files changed, evidence)

### M4 Done-when gates
- [x] `exec`-modified files stale earlier evidence (`TestExecModifiedFileStalesEarlierEvidence`)
- [x] Child env contains no API keys/tokens/secrets (`TestExecChildEnvContainsNoKeys`)
- [x] Untrusted gate blocks in headless and never fires in local flow (`TestUntrustedGateHeadlessVsLocal`)
- [x] SIGINT leaves no orphan processes (`TestExecCancelLeavesNoOrphanProcesses`)
- [x] Budget exhaustion never reports `completed` (`TestBudgetExhausted`)
- [x] Compaction at 75% and 85% loses nothing from `events.jsonl` (`TestCompaction75And85PreservesEventsJSONL`)
- [x] `go vet ./...`, `go test ./...`, and `CGO_ENABLED=0` release build pass (7.3 MiB)

## M5 Checklist — Eval harness (Merged)

- [x] `eval/`: task schema (`task.toml`: repo, commit, prompt, acceptance command, category), clean `git worktree` per run, alternating order (odd `jcode -> pu`, even `pu -> jcode`), same model/budget/timeout, append metrics to `results.jsonl`
- [x] `eval/`: report generator (success rates, median wall time, median notional cost & raw tokens per successful task, spec §12 Go/No-Go verdict)
- [x] `cmd/pu run`: hidden `--ablate=turn0,hashline,pinned-prefix,staged` switch
- [x] `eval/`: 3 sample tasks on a small toy repo (`eval/sample_tasks/`) + live dry run producing `results.jsonl` and report for both `pu` and `jcode`

### M5 Done-when gates
- [x] Dry run produces `results.jsonl` and report for `pu` (3/3 pass, median 21.2s, $0.000937 notional) and `jcode` (3/3 pass, median 73.8s)
- [x] `go vet ./...`, `go test ./...`, and `CGO_ENABLED=0` release build pass (7.3 MiB)

## Completed Milestones

### M1 — Skeleton and driver (merged)
- Built `go.mod`, `internal/catalog`, `internal/provider` (`openai-chat-compat` SSE + `x-opencode-session`), `internal/store`, `internal/exec` (`ScrubEnv`), `cmd/pu` (`doctor`).
- Live smoke test `TestSmokeOpenCodeGo` passed on `deepseek-v4.1-flash` (`reasoning_effort=low`).

### M2 — Loop, read, search (merged)
- Pre-M2 fixes: strict SSE EOF error without `[DONE]`/`finish_reason`, deterministic fallback tool-call ID (`call_<turn>_<index>`), `go mod tidy`.
- Built `internal/core` (embedded `system.txt`, 4-layer KV-invariant prefix, Turn-0 fingerprint, single-writer loop, `errgroup` parallel tools, 64 KiB spill, budget limits), `internal/edit` (anchored `read` + revision tracker), `internal/search` (`rg` + `WalkDir` fallback), `cmd/pu run`.
- Live 2-turn tool-call smoke test confirmed `deepseek-v4.1-flash` accepts `reasoning_content` in history (optional).

### M3 — Edit and undo (merged)
- Built `internal/edit/patch.go` (`hashline`, `search_replace`, `full_rewrite`, `create`, `delete`, strict revision/anchor lock with fresh-anchored rejection and ≤50 KiB fresh-anchored diff), `internal/edit/checkpoint.go` (`.pu/checkpoints/<session>/` + `manifest.json`, `UndoSession`, `DiffSession`), registered `patch` in `internal/core`, and wired `pu diff`/`pu undo` in `cmd/pu`.
- Gates: `go vet ./...` PASS; `go test -v ./...` PASS (all 5 M3 gate tests in `internal/edit/patch_test.go` green); `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pu ./cmd/pu` PASS (7.2 MiB).

### M4 — Exec, verification, stopping (merged)
- Built `internal/exec/run.go` (pipe `Run`, `Setpgid`, scrubbed env, 64 KiB cap, SIGTERM -> 300ms -> SIGKILL teardown), `internal/edit` post-`exec` `CheckExternalMutations`, `internal/plan` (`.pu/plan.json` mirror + revision-stale evidence), `internal/core/compaction.go` (75% soft trim + 85% clean state handoff), untrusted external-content gate, open-step continuations (max 2 then `failed`), and `cmd/pu run` signal/flag wiring.
- Gates: `go vet ./...` PASS; `go test -v ./...` PASS (`TestExecChildEnvContainsNoKeys`, `TestExecCancelLeavesNoOrphanProcesses`, `TestExecModifiedFileStalesEarlierEvidence`, `TestUntrustedGateHeadlessVsLocal`, `TestOpenStepContinuationsThenFailed`, `TestCompaction75And85PreservesEventsJSONL`, `TestBudgetExhausted`); `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pu ./cmd/pu` PASS (7.3 MiB).

### M5 — Eval harness (merged)
- Built `eval/` (`task.go`, `runner.go`, `report.go`, `eval_test.go`, `eval/cmd/pu-eval/main.go`) with clean `git worktree` isolation, odd/even alternating order (`jcode -> pu` vs `pu -> jcode`), `pu` and `jcode` adapters (`jcode run --json -p opencode-go -m <model> -C <worktree> --no-update --no-selfdev --quiet`), apples-to-apples notional cost calculation from `models.toml` when `jcode` omits `cost_usd`, spec §12 Go/No-Go report, hidden `--ablate=turn0,hashline,pinned-prefix,staged` switch, and 3 sample tasks on nested toy module `eval/sample_tasks/toyrepo/`.
- Gates: `go vet ./...` PASS; `go test ./...` PASS (`TestAlternatingToolOrder`, `TestSummarizeGoNoGoVerdict`, `TestDryRunWorktreesAlternatingAndResultsJSONL`); `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pu ./cmd/pu` PASS (7.3 MiB).
- Live 3-task dry run (`deepseek-v4.1-flash`, `reasoning_effort=low`): `pu` 3/3 (100.0%, median wall 21,231 ms, median notional cost $0.000937, median tokens 8416/3840/399); `jcode` 3/3 (100.0%, median wall 73,756 ms, median tokens 12637/12416/111).

### M5 Eval Fairness (merged)
- Fixed all 5 eval fairness items on `p0/m5-eval-fairness`:
  1. **External wall time for both tools**: `wall_time_ms` now measures external process start-to-exit for both `pu` and `jcode`; `pu`'s self-reported wall time is recorded separately in `internal_wall_time_ms`.
  2. **Reasoning effort parity**: `jcode run` has no CLI reasoning-effort flag; the harness writes `openai_reasoning_effort = "<effort>"` into each run's isolated `JCODE_HOME/config.toml` as best effort, and records `reasoning_effort`, `reasoning_enforced` (`true` for `pu`, `false` for `jcode`), and `reasoning_note` in `results.jsonl` and the report.
  3. **Isolated `jcode` home/config per run**: Each `jcode` invocation runs with a fresh temp `JCODE_HOME` + `XDG_CONFIG_HOME`/`XDG_DATA_HOME`/`XDG_CACHE_HOME`, `memory_sidecar_enabled = false`, `JCODE_NO_TELEMETRY=1`, and `OPENCODE_GO_API_KEY` forwarded from `OPENCODE_API_KEY` when unset so zero memory or sessions carry between tasks. Also prevented crashed/failed agent runs (`agent_status == "failed"` / `"setup_error"`) from falsely passing `no_change` acceptance checks.
  4. **Budget limits in `results.jsonl` and report**: Added `budget` (`max_cost_usd`, `max_turns`, `timeout_ms`, `cost_cap_enforced`, `turn_cap_enforced`, `timeout_enforced`) to every `Result`; `FormatReport` explicitly notes that `jcode` has no CLI cost/turn cap (harness timeout only).
  5. **Rate-limit detection + `--resume`**: `IsRateLimited` detects HTTP 429 and OpenCode Go quota/5-hour/weekly/monthly limit errors, marks runs `agent_status: "rate_limited"` (`rate_limited: true`), excludes them from pass/fail counts and medians in `Summarize`/`FormatReport`, and `--resume` skips already-completed `(task_id, tool)` entries while retrying `rate_limited` ones.
- Fair live 3-task alternating dry run (`deepseek-v4.1-flash`, `reasoning_effort=low`, external wall time, isolated `JCODE_HOME`, `max_cost_usd=0.20`, `max_turns=12`, `timeout=2m`):
  - `pu`: **3/3 (100.0%)**, median external wall **39,599 ms** (internal 39,579 ms), median notional cost **$0.000952**, median tokens (in/cached/out) **8,408 / 3,712 / 473** (cumulative across turns).
  - `jcode`: **3/3 (100.0%)**, median external wall **60,826 ms**, median notional cost **$0.000262**, median tokens (in/cached/out) **11,694 / 11,264 / 250** (note: `jcode --json` emits last-turn usage rather than cumulative across turns).
  - Spec §12 Verdict on 3-task dry run: **GO** (`pu` 100.0% >= `jcode` 100.0%; median external wall-time gain **34.9%** >= 15%).
- Gates: `go vet ./...` PASS; `go test ./...` PASS (including `TestRateLimitDetectionExclusionAndResume`, `TestReportNotesJcodeMissingCostTurnCap`); `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pu ./cmd/pu` PASS.

### M5 Eval Parity (merged)
- Implemented 3 pre-pilot parity fixes on `p0/m5-eval-parity`:
  1. **Cost parity (`jcode` multi-turn usage):** Added `ParseJcodeSessionUsage` in `eval/runner.go` to sum `token_usage` across all assistant turns from `sessions/*.journal.jsonl` and `sessions/*.json` (deduplicated by message ID, with `logs/jcode-*.log` fallback) in each run's isolated `JCODE_HOME`, recomputing notional list-price cost from `models.toml`. Verified with `TestJcodeMultiTurnSessionUsageParity` (fake 3-turn `jcode` script whose stdout only emits turn 3).
  2. **`pu` wall-time gap audit:** Captured `processStart := time.Now()` at package init in `cmd/pu/main.go` and passed `ProcessStart` into `core.Config` so `pu`'s `internal_wall_time_ms` covers the full CLI lifecycle (flags, `models.toml` load, Turn-0, loop, shutdown). Per-run external vs internal gap is only 18–31 ms (OS process launch + final JSON flush/exit; no slow startup path found).
  3. **Reasoning parity:** Inspected `jcode`'s isolated `JCODE_HOME` session journal (`"reasoning_effort": null`) and log (`PROVIDER_CANONICAL_INPUT ... thinking_enabled=None provider_features=false`) with `openai_reasoning_effort = "low"` configured; confirmed `jcode` on `opencode-go` ignores `openai_reasoning_effort` and always uses the provider default. Added `--reasoning-effort` to `pu run` and defaulted `pu-eval` to `provider_default` so both tools use the provider default for the pilot.
- Live 3-task alternating dry run (`deepseek-v4.1-flash`, `provider_default`, isolated `JCODE_HOME`, `max_cost_usd=0.20`, `max_turns=12`, `timeout=2m`):
  - `01_fix_sub`: `pu` PASS (5 turns, ext 37,263 ms / int 37,245 ms, gap 18 ms, $0.000788) | `jcode` FAIL timeout (1 turn, ext 120,041 ms, $0.001555)
  - `02_fix_clamp`: `pu` PASS (4 turns, ext 27,151 ms / int 27,133 ms, gap 18 ms, $0.001322) | `jcode` PASS (7 turns, ext 80,095 ms, 78,026/65,408/1,223 tok, $0.002823)
  - `03_no_change`: `pu` PASS (3 turns, ext 21,107 ms / int 21,076 ms, gap 31 ms, $0.000608) | `jcode` PASS (5 turns, ext 53,020 ms, 51,458/39,936/656 tok, $0.002242)
  - Summary: `pu` **3/3 (100.0%)**, median wall **27,151 ms**, median notional **$0.000788** vs `jcode` **2/3 (66.7%)**, median wall **66,558 ms**, median notional **$0.002532** — Spec §12 Verdict: **GO** (+59.2% wall-time gain, +68.9% notional cost gain).
- Gates: `go vet ./...` PASS; `go test ./...` PASS; `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pu ./cmd/pu` PASS.

### Phase 1 — jcode Timeout Investigation & Template Pre-seed (merged)
- **Root cause of 120s timeout:** Both `pu` and `jcode` receive the exact same `context.WithTimeout(ctx, cfg.Timeout)` in `eval/runner.go` (`--timeout 2m` = 120,000 ms). `jcode`'s isolated log (`logs/jcode-*.log`) showed `HTTP connection established in 119925ms` on OpenCode Go's endpoint during upstream latency spikes (not a stdin hang or local deadlock).
- **Harness hardening (`p0/m5-jcode-timeout`):**
  - Attached explicit `/dev/null` to `cmd.Stdin` for both `runPu` and `runJcode`.
  - Added `prepareJcodeTemplateHome` / `seedJcodeHome`: creates one pre-seeded template `JCODE_HOME` per `RunAll` (memory disabled, migration stamps, allowlisted static caches `opencode-go_models.json` and `models_dev_pricing.json`, zero session history or memory DBs) and copies it into a fresh temp dir per `jcode` run. Verified `pu` has zero first-run cache/network work (17–31 ms ext-vs-int overhead).
- **`01_fix_sub` 3x per tool (`deepseek-v4.1-flash`, `provider_default`, `timeout=3m`, alternating):**
  - `r1`: `jcode` FAIL timeout (5 turns, 180,055 ms, $0.004070) | `pu` PASS (4 turns, 180,085 ms ext / 180,054 ms int, `budget_exhausted` after fix, $0.000937)
  - `r2`: `pu` PASS (3 turns, 180,064 ms ext / 180,050 ms int, `budget_exhausted` after fix, $0.000648) | `jcode` PASS (9 turns, 95,036 ms, $0.004699)
  - `r3`: `jcode` PASS (9 turns, 79,880 ms, $0.003089) | `pu` PASS (8 turns, 52,224 ms ext / 52,207 ms int, $0.002025)
  - Finding: OpenCode Go upstream latency spikes affect both tools equally (`r1`/`r2` hit ~45–60s/turn; `r3` dropped to ~6–9s/turn); `pu` still uses ~2–5x fewer tokens/cost per task.
- Gates: `go vet ./...` PASS; `go test ./...` PASS; `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pu ./cmd/pu` PASS.

## Pilot results (interim — 10/30 paired tasks completed before 17:10 cutoff; resumable via `--resume`)

- **Run configuration:** `eval/pilot_tasks` (30 frozen real-history tasks, `SELECTION.md`), `deepseek-v4.1-flash` on OpenCode Go, `provider_default` reasoning for both tools, alternating order (odd `jcode -> pu`, even `pu -> jcode`), `--timeout 3m`, `--max-turns 20`, `--max-cost-usd 0.50` (`pu` only; `jcode` has no CLI cost/turn cap), pre-seeded isolated `JCODE_HOME` + `stdin=/dev/null` for both tools. Raw results saved in `eval/pilot_results.jsonl` (21 runs: tasks 01–10 paired + task 11 `jcode`; resume with `pu-eval --resume --tasks eval/pilot_tasks --out eval/pilot_results.jsonl`).
- **Headline metrics (all completed runs so far):**
  - `pu`: **5/10 (50.0%)** pass, median wall **180,015 ms**, median notional cost **$0.006525** (median tokens: 185,872 in / 166,656 cached / 4,727 out).
  - `jcode`: **2/11 (18.2%)** pass (**2/10 = 20.0%** on the 10 paired tasks), median wall **152,536 ms**, median notional cost **$0.008862** (median tokens: 346,154 in / 316,416 cached / 5,754 out).
- **Spec §12 Go/No-Go verdict (interim):** **GO** — `pu` success **50.0% >= `jcode` 18.2%** (20.0% paired), median notional cost gain **+26.4%** (>= 15% threshold met; median wall-time gain -18.0% because 3 of `pu`'s 5 passes hit the 180s timeout *after* fixing the code while `jcode` timed out without passing). Full 30-task verdict pending `--resume` of tasks 11–30.
- **Per-category breakdown:**
  - `bugfix` (10/10 paired): `pu` **5/10 (50.0%)**, median wall 180,015 ms, median notional $0.006525 | `jcode` **2/10 (20.0%)**, median wall 152,536 ms, median notional $0.008862.
  - `feature_refactor` (0/10 paired; 1 `jcode` run done): `jcode` **0/1 (0.0%)** (timeout on `11_tetik2_recall_lessons`); `pu` pending `--resume`.
  - `test_build` (0/5), `no_change` (0/3), `large_file` (0/2): pending `--resume` (stopped cleanly at 16:59 Istanbul time to meet the 17:10 wrap-up deadline).
- **Rate-limited and invalid runs:** **0** `rate_limited`, **0** `setup_error`/invalid runs.
- **Top 5 tasks with the biggest gap:**
  1. `08_gjson_uint_overflow` (`pu` win): `pu` PASS in 143.2s / 11 turns / $0.00612 vs `jcode` FAIL timeout at 180.0s / 16 turns / $0.01288 (2.1x cheaper, finished cleanly).
  2. `06_gjson_zero_prefix` (`pu` win): `pu` PASS in 152.2s / 16 turns / $0.00599 vs `jcode` FAIL timeout at 180.0s / 17 turns / $0.00729.
  3. `01_lipgloss_table_nil` (`pu` win): `pu` PASS (`budget_exhausted` at 180.1s after applying the fix, 17 turns, $0.00918) vs `jcode` FAIL timeout at 180.2s (7 turns, $0.00585, fix incomplete).
  4. `03_lipgloss_alpha_enum` (`jcode` win): `jcode` PASS in 154.0s / 15 turns / $0.00993 vs `pu` FAIL (`signal: killed` at 180.0s before flushing JSON report).
  5. `05_lipgloss_unset_underline` (`jcode` win): `jcode` PASS in 151.0s / 18 turns / $0.00779 vs `pu` FAIL (`signal: killed` at 180.0s before flushing JSON report).

## Decisions

- **OpenCode Go endpoint path & session header**: Chat-completions models (including pilot model `deepseek-v4.1-flash`) use `https://opencode.ai/inference/go/openai/v1`, `reasoning_content`, and require `x-opencode-session` (`provider.Request.SessionID`).
- **Confirmed pilot model & reasoning parity**: `deepseek-v4.1-flash` on OpenCode Go (`openai-chat-compat`) using **provider default** reasoning for both `pu` and `jcode` (verified from `jcode`'s session journal & debug log that `jcode` on `opencode-go` ignores `openai_reasoning_effort` and sends `thinking_enabled=None`).
- **`reasoning_content` in assistant history on `deepseek-v4.1-flash`**: **Accepted (optional)** (verified live via `TestSmokeTwoTurnToolCallReasoningHistory`). Preserving `ReasoningPart` in assistant history succeeds and retains KV cache hits.
- **Notional list-price cost vs subscription billing**: Local USD cost computed from `models.toml` prices is **notional list-price cost**; every report and journal usage event records **raw token counts** (`input_tokens`, `cached_input_tokens`, `output_tokens`) alongside it.
- **Deterministic tool-call fallback ID**: Empty streamed tool-call IDs receive `call_<turn>_<index>`.
- **Anchored read & revision table location**: 5-letter lowercase ASCII anchors (`[a-z]{5}`, unique per file even for identical lines) and SHA-256 revision tracking live in `internal/edit` so `read` and `patch` share one source of truth.
- **Layer 3 project rules order**: `AGENTS.md`, then `PU.md` (read once at session start, never mutated mid-session).
- **Output spill threshold**: Tool outputs > 64 KiB spill to `.pu/sessions/<id>/out/<call_id>.txt`.
- **jcode eval adapter & notional cost**: Verified `jcode run --help` on installed `jcode`; adapter invokes `jcode run --json -p opencode-go -m <model> -C <worktree> --no-update --no-selfdev --quiet`. When `jcode` reports raw tokens without `cost_usd`, `eval/runner.go` computes notional list-price cost from `models.toml` using the exact same formula as `pu`.
- **Nested toyrepo module**: `eval/sample_tasks/toyrepo/` has its own `go.mod` so intentionally failing sample tests never interfere with root `go test ./...`.

## Open questions

- **Pilot completion (`--resume` tasks 11–30):** 21/60 runs (tasks 01–10 paired + task 11 `jcode`) finished before the 17:10 Istanbul wrap-up deadline; resume with `pu-eval --resume --tasks eval/pilot_tasks --out eval/pilot_results.jsonl --timeout 5m` (consider 5m timeout because under `provider_default` full thinking on OpenCode Go, 9/11 `jcode` runs and 8/10 `pu` runs hit the 3m cap).
- **pu improvement idea — graceful shutdown cushion before harness `SIGKILL`:** On `03_lipgloss_alpha_enum`, `05_lipgloss_unset_underline`, and `07_gjson_uint_20byte`, `pu` was killed by `exec.CommandContext` at 180s before its own `--timeout 3m` flushed the JSON report (`turns=0`, `$0` recorded). Give `pu` a 2s internal deadline cushion (or have `pu-eval` send `SIGINT` before `SIGKILL`) so partial turn/token telemetry is always preserved.
- **pu improvement idea — stop earlier once targeted tests pass:** On `01_lipgloss_table_nil`, `02_lipgloss_tree_nil`, and `09_serde_json_f32_eq`, `pu` fixed the bug (passing acceptance) but kept running extra turns/checks until the 180s timeout (`budget_exhausted`). Tightening completion behavior after green targeted tests should cut median wall time and notional cost further.
- **OpenCode Go 5-hour / weekly usage limits during 30-task pilot:** Handled via `rate_limited` classification + `pu-eval --resume` (0 rate-limited runs observed across the first 21 runs).
