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

## Pilot v0 (3m cap, invalid)

- **Status:** Invalid — 16 of 21 runs hit the 3m timeout, so v0 measured the cap rather than the agents. Archived at `eval/pilot_v0_3m_results.jsonl` and not resumed.
- **Run configuration:** `eval/pilot_tasks` (30 frozen real-history tasks, `SELECTION.md`), `deepseek-v4.1-flash` on OpenCode Go, `provider_default` reasoning for both tools, alternating order (odd `jcode -> pu`, even `pu -> jcode`), `--timeout 3m`, `--max-turns 20`, `--max-cost-usd 0.50` (`pu` only; `jcode` has no CLI cost/turn cap), pre-seeded isolated `JCODE_HOME` + `stdin=/dev/null` for both tools. Raw results archived in `eval/pilot_v0_3m_results.jsonl` (21 runs: tasks 01–10 paired + task 11 `jcode`).
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

### Pilot v1 Phase 1 — Harness Fixes (merged)
- **Archived v0:** Renamed `eval/pilot_results.jsonl` -> `eval/pilot_v0_3m_results.jsonl` and labeled v0 invalid (3m cap).
- **Graceful timeouts (`eval/runner.go`):** Set harness timeout to `toolTimeout + 30s` (`HarnessTimeoutMS` recorded in `budget`). On expiry, harness sends `SIGINT` to the process group (`-pgid`), waits `10s`, then sends `SIGKILL` (`runCmdWithGracefulInterrupt`), so `pu` always flushes its JSON report. Added `timed_out` tracking and `TestGracefulInterruptTimeoutFlushesReport`.
- **`jcode` 1-turn stall diagnosis (07 / 11):** Captured `jcode` log/journal on `07_gjson_uint_20byte` with preserved isolated `JCODE_HOME`: `jcode` executed tools normally, opened an HTTPS SSE stream (`status=200 OK` on `https://opencode.ai/zen/go/v1`), and then sat idle inside its SSE stream reader waiting on upstream OpenCode Go stream chunks (not a harness stdin, tool-call, or prompt hang). Cause is in `jcode`/upstream SSE handling; 15m timeout in v1 gives full headroom.
- **Concurrency & scoring (`eval/runner.go`, `eval/cmd/pu-eval/main.go`):** Run `pu` and `jcode` concurrently per task in separate git worktrees, with `--parallel 2` (max 2 tasks in parallel = 4 concurrent processes), preserving `order_index` in `results.jsonl`. Acceptance pass alone determines `passed` (excluding `rate_limited` and `setup_error`), and any `rate_limited` run halts scheduling of remaining tasks. Verified with `TestConcurrentTwoTasksTwoTools`.
- Gates: `go vet ./...` PASS; `go test ./...` PASS; release build PASS.

### Pilot v1 Phase 2 — Frozen `pu` Changes (merged)
- **Frozen `pu` commit:** `1c72639` (merged to `main` at `39e1bd1`; binary rebuilt with `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o pu ./cmd/pu`, 7.3 MiB). No `pu` code changes allowed during Pilot v1.
- **Changes:**
  1. Immediate completion on green targeted check (`internal/core/loop.go`, `internal/core/system.txt`): once a file has been modified, a targeted verification `exec` passes (`exitCode == 0`, `Validity == "current"`), and `planMgr.OpenSteps()` is empty, `pu` completes immediately instead of running extra redundant checks (`TestEarlyCompleteOnGreenCheckWithNoOpenSteps`).
  2. 2s internal timeout cushion (`internal/core/loop.go`): when `cfg.Timeout > 4s`, `runCtx` expires 2s before `--timeout` so `pu` always finishes cleanly and flushes its JSON report.
- **Verification (unit tests + `eval/sample_tasks` ONLY; never run on pilot tasks):** `go vet ./...` PASS; `go test ./...` PASS; `eval/sample_tasks` `pu` 3/3 (100.0%, 0 timeouts, median wall 12.2s, median notional $0.000628).

## Pilot v1 results (30/30 paired tasks — valid)

- **Run configuration:** All 30 frozen tasks in `eval/pilot_tasks` (`SELECTION.md`), frozen `pu` commit `1c72639` (`39e1bd1` on `main`), `deepseek-v4.1-flash` on OpenCode Go, `provider_default` reasoning for both tools, `--timeout 15m` (`harness_timeout_ms = 930000`, `SIGINT -> 10s -> SIGKILL`), `--max-turns 40`, `--max-cost-usd 0.50` (`pu` only; `jcode` has no CLI cost/turn cap), `--parallel 2` (2 tasks × 2 tools concurrent in separate worktrees), pre-seeded isolated `JCODE_HOME` + `stdin=/dev/null`. Raw results in `eval/pilot_v1_results.jsonl` (63 lines: 60 valid paired runs + 3 transient `rate_limited` runs retried via `--resume`).
- **What changed between v0 and v1:**
  - Tool timeout increased from `3m` (where 16/21 runs timed out) to `15m` (`harness_timeout = 15m30s`, `SIGINT -> 10s -> SIGKILL`).
  - `pu` max turns increased from `20` to `40`; `pu` gained a 2s internal timeout cushion and immediate completion on passing targeted checks (`1c72639`).
  - Harness runs `pu` and `jcode` concurrently per task (max 2 tasks in parallel = 4 processes), tracks `timed_out` separately, and scores `passed` strictly by acceptance exit code.
- **Timeout parity check:** Verified across all 60 valid runs in `eval/pilot_v1_results.jsonl`: both `pu` and `jcode` ran with `timeout_ms = 900000` (15m) and `harness_timeout_ms = 930000` (15m30s).
- **Headline metrics (30/30 paired tasks):**
  - `pu`: **22/30 (73.3%)** pass, **0 timeouts**, median wall **83,307 ms** (83.3s), median notional cost **$0.006239** (median tokens: 130,529 in / 103,872 cached / 3,990 out).
  - `jcode`: **29/30 (96.7%)** pass, **0 timeouts**, median wall **242,647 ms** (242.6s), median notional cost **$0.011584** (median tokens: 490,506 in / 455,552 cached / 6,527 out).
  - Efficiency on passing tasks: `pu` median wall time **65.7% faster** (2.9x), median notional cost **46.1% lower** (1.86x).
- **Spec §12 Go/No-Go verdict (30/30 paired >= 25 threshold):** **NO-GO** — `pu` success rate **73.3% < `jcode` 96.7%** (despite beating the 15% wall-time and cost thresholds by wide margins on passing tasks).
- **Per-category breakdown:**
  - `bugfix` (10 paired): `pu` **8/10 (80.0%)**, 0 timeouts, med wall 191.8s, med cost $0.007747 | `jcode` **10/10 (100.0%)**, 0 timeouts, med wall 268.7s, med cost $0.012004.
  - `feature_refactor` (10 paired): `pu` **5/10 (50.0%)**, 0 timeouts, med wall 80.3s, med cost $0.008087 | `jcode` **10/10 (100.0%)**, 0 timeouts, med wall 247.9s, med cost $0.011849.
  - `test_build` (5 paired): `pu` **4/5 (80.0%)**, 0 timeouts, med wall 53.4s, med cost $0.003753 | `jcode` **4/5 (80.0%)**, 0 timeouts, med wall 125.7s, med cost $0.007501.
  - `no_change` (3 paired): `pu` **3/3 (100.0%)**, 0 timeouts, med wall 69.4s, med cost $0.004098 | `jcode` **3/3 (100.0%)**, 0 timeouts, med wall 154.5s, med cost $0.014227.
  - `large_file` (2 paired): `pu` **2/2 (100.0%)**, 0 timeouts, med wall 95.0s, med cost $0.005199 | `jcode` **2/2 (100.0%)**, 0 timeouts, med wall 137.4s, med cost $0.009198.
- **Rate-limited and invalid runs:** **3** transient `rate_limited` runs (`08_gjson_uint_overflow` `pu` ×1, `17_cobra_default_comp` `pu` ×2 during 4-way concurrency bursts; excluded from metrics and retried to completion via `--resume`), **0** `setup_error`/invalid runs.
- **Top 5 tasks with the biggest gap:**
  1. `04_lipgloss_tree_offset` (`jcode` win, correctness): `pu` stopped at turn 7 (48.7s) after Phase 2 early-completion fired on an intermediate passing `go test` before edge cases were fixed, while `jcode` passed in 214.0s.
  2. `11_tetik2_recall_lessons` (`jcode` win, correctness): `pu` terminated early at turn 15 (176.1s) on a partial multi-file edit whose existing tests still passed, while `jcode` completed all files and passed in 318.6s.
  3. `21_serde_json_f64_warn` (`jcode` win, correctness): `pu` stopped at turn 10 (131.9s) after a plain `cargo test` passed without `-D warnings`, while `jcode` ran `RUSTFLAGS='-D warnings'` and passed in 248.8s.
  4. `22_tetik_xtask_archive` (`pu` win, correctness + speed): `pu` passed in 59.6s / 8 turns / $0.00454 while `jcode` failed in 97.4s / 15 turns / $0.00791.
  5. `02_lipgloss_tree_nil` (`pu` win, speed + cost): both passed, with `pu` finishing 303.6s faster (158.9s vs 462.5s) and 31% cheaper ($0.01525 vs $0.02221); similarly `28_gjson_no_change_path` (`pu` 117.7s / $0.00829 vs `jcode` 420.4s / $0.03222, 3.6x faster and 3.9x cheaper).

## pu freeze v2 (evidence rule + 429 backoff)

- **Motivation:** All 8 `pu` failures in Pilot v1 ended `agent_status=completed` with red acceptance (false completion), several not early (01: 432s, 19: 35 turns). `pu` stopped once an existing test was green, but nothing checked the new behaviour.
- **Changes (`p0/pu-evidence`, commit `94eca17`):**
  1. Removed Pilot v1's hard stop after a green `exec` (`passedCheckInTurn`/`isVerificationCmd` deleted). Back to the M4 rule: `completed` needs current evidence for every plan step, with up to 2 continuations.
  2. Evidence must cover the change (`internal/plan`): once `pu` has patched files, a done step only counts as covered by a current passing check that either went red -> green in this session (same normalised command), or matches the step's new optional `check` field (an existing test the step names). A check that was only ever green doesn't count. One line added to Layer 1 (`system.txt`) and the continuation message.
  3. `internal/provider`: retries HTTP 429/529 inside one `Stream` call (so the wait doesn't count as a turn). Honours `Retry-After` (seconds or HTTP date, capped at 30s), otherwise uses jittered exponential backoff `[0.5,1) x 1s x 2^n`, at most 3 retries.
- **Tests:** `TestAlreadyGreenCheckDoesNotCoverChangedBehaviour`, `TestRedThenGreenCoversStep`, `TestNamedExistingCheckCoversStep`, `TestNoEditsAnyGreenCheckCounts`, `TestStreamRetries429ThenSucceeds`, `TestStreamGivesUpAfterThreeRetries`, `TestRetryDelayJitterBounds`. Removed obsolete `TestEarlyCompleteOnGreenCheckWithNoOpenSteps`.
- **Verification:** `go vet` PASS, `go test ./...` PASS, release build OK. `eval/sample_tasks` `pu` 3/3. **Dev check on the 8 v1 failures (`pu` only): 5/8 now pass** (01, 12, 17, 19, 21). 04, 11 and 13 still fail, and all three still end as false completions. These tasks were used only as a dev check; no tuning was done on them after the run.
- **Frozen `pu` commit for Pilot v2: `94eca17`.**

## Pilot v2 results (30/30 paired held-out tasks, valid)

- **Run:** `eval/pilot_v2_tasks` (frozen `e376391` before any v2 run), `pu` frozen at `94eca17`, same settings as v1: 15m timeout (harness 15m30s, SIGINT then SIGKILL), 40 turns, $0.50 cap for `pu`, `provider_default`, `pu`+`jcode` concurrent per task, at most 2 tasks in parallel. Raw data: `eval/pilot_v2_results.jsonl`, analysis: `eval/analyze_v2.py`. 60/60 valid runs, **0 rate-limited, 0 setup errors, 0 timeouts**. Timeout parity verified: every row has `timeout_ms=900000`, `harness_timeout_ms=930000`. Ran 18:20-19:03 Istanbul.
- **Success rate:** `pu` **29/30 (96.7%)**, `jcode` **29/30 (96.7%)** (v1: 73.3% vs 96.7%).
- **Paired comparison on the 28 tasks both passed:** median per-task `pu/jcode` ratio **wall 0.838** (pu 16% faster), **cost 0.952** (pu 5% cheaper). `pu` was faster on 16/28 tasks, cheaper on 19/28, and both on 15/28.
- **Unpaired medians (all runs):** wall `pu` 101.1s vs `jcode` 116.5s (13.2% faster); notional cost `pu` $0.007901 vs `jcode` $0.009217 (14.3% lower).
- **False completions (agent said completed, acceptance red):** `pu` **1** (`19_toml_add_decoder_maxtablenest`, 30 turns), `jcode` **1** (`03_lipgloss_fix_color_expand_rgbcolo`). In v1, `pu` had 8.
- **Per category (pass, false completions, median wall/cost on passes):**
  - `bugfix` (10): `pu` 10/10, fc 0, 93.3s, $0.0073 | `jcode` 9/10, fc 1, 117.4s, $0.0087
  - `feature_refactor` (10): `pu` 9/10, fc 1, 120.6s, $0.0094 | `jcode` 10/10, fc 0, 116.4s, $0.0090
  - `test_build` (5): `pu` 5/5, 52.7s, $0.0062 | `jcode` 5/5, 104.6s, $0.0077
  - `no_change` (3): `pu` 3/3, 186.5s, $0.0142 | `jcode` 3/3, 165.7s, $0.0132
  - `large_file` (2): `pu` 2/2, 174.2s, $0.0104 | `jcode` 2/2, 118.9s, $0.0105
- **Spec §12 verdict (30 paired, at least 25):** **NO-GO, narrowly.** `pu` matches `jcode` on success (96.7% each), but the spec's unpaired median gains are 13.2% (wall) and 14.3% (cost), both short of the 15% threshold. On the paired per-task ratio, `pu` is 16.2% faster, but §12 is defined on medians, so the verdict stays NO-GO.
- **Caveats:** One run per tool per task, so a 1-2 point swing is within noise. `pu` gives up speed on `no_change` and `large_file` (more verification turns under the new evidence rule).

## v2 repeat: pre-registered rule

Written and committed before any repeat run. `pu` stays frozen at `94eca17`; the task set stays `eval/pilot_v2_tasks` (`e376391`).

- **Runs:** each tool runs each v2 task 3 times in total: rep 1 = existing `eval/pilot_v2_results.jsonl`, reps 2 and 3 = new runs in `eval/pilot_v2_repeat_results.jsonl` (field `rep`). Same settings as v2: 15m timeout, 40 turns, $0.50 cap for `pu`, `provider_default`, concurrent pairs, `--parallel 2`, `--resume`.
- **Pass:** a task counts as passed for a tool if it passes in at least 2 of its 3 runs.
- **Per-task metrics:** per task and per tool, take the median wall time and median notional cost over the runs that passed.
- **Paired comparison:** use tasks both tools passed (under the 2/3 rule). For each, compute the `pu/jcode` ratio of the per-task medians.
- **Verdict (spec §12 bar):** **GO** if `pu`'s 2/3 success rate >= `jcode`'s AND (median per-task wall ratio <= 0.85 OR median per-task cost ratio <= 0.85); otherwise **NO-GO**. Requires >= 25 tasks with 3 valid runs per tool, else **INCOMPLETE**.
- **Rate-limited/setup-error runs** are excluded and retried via `--resume`; they don't count as one of the 3.
- **Secondary (doesn't decide the verdict):** per tool, total wall time and total notional cost across all runs (failures included) divided by the number of passed runs.

## Pilot v2 repeat results (3 runs x 30 tasks x 2 tools, valid)

- **Data:** rep 1 = `eval/pilot_v2_results.jsonl`, reps 2-3 = `eval/pilot_v2_repeat_results.jsonl` (`rep` field, added to the harness only via `--rep`; `pu` binary rebuilt from `94eca17`). 180/180 valid runs, 0 rate-limited, 0 timeouts. Ran 23:11-00:26 Istanbul. Analysis: `eval/analyze_repeat.py`.
- **Verdict under the pre-registered rule: NO-GO.** 2/3 success: `pu` **28/30 (93.3%)**, `jcode` **29/30 (96.7%)**. On the 27 tasks both passed, the median per-task `pu/jcode` ratio is **wall 0.847** (just inside the 0.85 bar) and **cost 0.931**. `pu` was faster on 19/27 and cheaper on 17/27. The speed bar is met, but the success condition fails by one task.
- **Tasks that decided it:** `pu` lost `03_lipgloss` (PFF; `jcode` FPP) and `19_toml_maxtablenest` (FPF). `jcode` lost `14_lipgloss_table_height` (PFF).
- **Run-to-run spread:**
  - Per-rep pass counts: `pu` 29, 29, 26; `jcode` 29, 28, 29.
  - Flaky tasks (1-2 of 3 passing): `pu` 4, `jcode` 3.
  - Median per-task coefficient of variation of wall time on passing runs: `pu` 0.33, `jcode` 0.19, so `pu` is noisier.
  - One run's pass or fail flips a single-rep verdict, so v2's single run said 29 vs 29.
- **False completions (agent said completed, acceptance red):** `pu` **5/90 runs**, `jcode` **4/90 runs**. One more `pu` run (26 rep 3) ended `failed` (open steps after continuations) and isn't counted as a false completion.
- **Per category (2/3 pass):** bugfix `pu` 9 / `jcode` 10; feature_refactor 9 / 9; test_build 5 / 5; no_change 3 / 3; large_file 2 / 2.
- **Median turns (all runs):** `pu` 15, `jcode` 17.
- **Secondary (all runs, failures included, divided by passed runs):** `pu` **115.3s, $0.01115** per pass; `jcode` **137.8s, $0.01231** per pass, so `pu` is 16% less time and 9% less cost per pass.

## Phase B: turn breakdown and turn cut (v1, v2 are dev sets)

- **Turn breakdown method:** `eval/turn_breakdown.py` classifies every model turn from `pu`'s `events.jsonl` by its tool calls. Precedence: continuation, edit (`patch`), test exec (before the first edit, after it, or full suite if >1 package/crate reported), git history (`git log/show` output), build/lint (exit line only), shell read (cat/grep/sed via exec), `read`, `search`, `plan`, final message. v2 session logs were regenerated with frozen `94eca17` (`pu` only, 1 run, `--sessions-dir`), because the harness had deleted them with the worktrees.
- **v2 breakdown, frozen `94eca17` (30 sessions, 556 turns, median 16.5/session):** shell_read 26.6% (4.9/session), git_history 16.4% (3.0), read 13.7% (2.5), edit 13.1% (2.4), full_suite 7.7% (1.4), test_before_edit 7.4% (1.4), final 5.6%, test_after_edit 5.4% (1.0), search 2.5%, build_lint 0.9%, continuation 0.4%, plan 0.4%. Exploration (shell_read + git_history + read + search) takes 59% of turns. Verification takes 21%, continuations under 1%.
- **Harness leak found and fixed (`eval/isolate.go`):** in 15/30 v2 sessions `pu` ran `git log/show` and read the upstream fix commit, because worktrees shared the source repo's refs. Each run now gets a fresh `git init` holding only the start commit's ancestry (fetched by SHA, no refs, tags or remotes). Branch heads are fetched under `refs/pu-eval-accept/*` only after the agent exits, for acceptance. Covered by `TestIsolatedCheckoutHidesFutureCommitsAndAcceptanceSeesThem`. All 30 v2 tasks' acceptance prefixes still resolve. Some v1 serde_json/tetik acceptances use `sh -c`/cargo forms the smoke check can't parse; they fall back to a plain worktree if isolation fails. **Remaining limitation:** both agents have network access. In at least one dev session (`14_lipgloss`), `pu` fetched upstream over the network and found the fix. This applies equally to `jcode` and isn't blocked.
- **Change (`p0/turn-cut`, smallest that keeps the rule strict):**
  1. **Auto baseline (`internal/core/baseline.go`, `internal/edit/baseline.go`):** when a test command passes, the agent has changed both test files and non-test files, a done plan step still lacks covering evidence, and the command never failed in-session, `pu` reruns the same command in a temporary git worktree. That worktree is the current workspace with `pu`'s non-test changes reverted to the pre-edit checkpoint (tests kept). If it fails there, the passing evidence is marked as covering (`MarkFailedBefore`), the same "fails without the change, passes with it" proof as before, with no model turn spent. If it also passes there, nothing changes and the step stays open. The workspace is never touched.
  2. **Batching:** one Layer 1 prompt line says new tests needn't be run before the fix and independent checks should be combined in one exec.
  3. Tests: `TestAutoBaselineMarksNewTestThatFailsBeforeFix`, `TestAutoBaselineDoesNotCoverTestThatAlsoPassesBefore`, `TestAutoBaselineSkipsWithoutChangedTests`.
- **Verification:**
  - `go vet`/`go test ./...` PASS; `eval/sample_tasks` 3/3.
  - v2 dev, same isolated harness, `pu` only, 1 run: frozen `94eca17` **28/30**, median turns **19.5**, wall 81.5s. First turn-cut build (`d43f617`, baseline ran on every green test) 27/30, **21 turns**, wall 132s; its 87 "also passes" notes made the model re-verify. Final `e672110` (baseline only when a done step lacks coverage; silent when it also passes) **28/30**, **17 turns**, wall 84.2s, 1 false completion.
  - Breakdown for `e672110` vs control: test_before_edit 1.00 -> 0.77/session, test_after_edit 0.67 -> 0.47, full_suite 1.27 -> 1.20, read 2.23 -> 1.97. Exploration via shell/git is unchanged (6.6 -> 7.3 and 2.8 -> 3.0/session).
  - v1 dev with `d43f617`: 24/30. v1 wasn't rerun with `e672110`, to save time and quota.
- **Frozen `pu` commit for Pilot v3: `e672110`.**

## Pilot v3: pre-registered rule

Committed before any v3 run. Same rule as "v2 repeat: pre-registered rule": set `eval/pilot_v3_tasks` (`7885e46`), `pu` frozen at `e672110`, `jcode` as installed. 3 runs per task per tool (`--rep 1..3`) into `eval/pilot_v3_results.jsonl`, same settings as v2, isolated harness for both tools. Pass = at least 2 of 3. Per-task medians over passing runs. Paired = both passed. GO iff `pu` 2/3 success >= `jcode`'s AND (median per-task wall ratio <= 0.85 OR cost ratio <= 0.85). At least 25 tasks with 3 valid runs per tool, else INCOMPLETE. Secondary (doesn't decide the verdict): total wall/cost across all runs / passed runs.

## Pilot v3 results (3 runs x 30 held-out tasks x 2 tools, valid)

- **Data:** `eval/pilot_v3_results.jsonl` (180/180 valid runs, 0 rate-limited, 0 setup errors, 0 timeouts), isolated harness, `pu` `e672110`. Ran 02:05-04:08 Istanbul. Analysis: `eval/analyze_repeat.py`, `eval/turn_breakdown.py`.
- **Verdict under the pre-registered rule: NO-GO.** 2/3 success: `pu` **27/30 (90.0%)**, `jcode` **28/30 (93.3%)**. Speed and cost clear the bar by a wide margin: on the 26 tasks both passed, the median per-task `pu/jcode` ratio is **wall 0.700** and **cost 0.779**. `pu` was faster on 22/26 and cheaper on 18/26. The verdict fails only on success rate (one task).
- **Decisive tasks:**
  - `pu` lost `14_cli_configured_c` (PFF) and `25_cli_complete_arguments` (FFF; `jcode` PPP). On 25, `pu` declared completion all 3 times after 28-38 turns and 173-429s.
  - `jcode` lost `11_afero_zipfs` (FPF, `pu` FPP).
  - Both lost `02_afero_unionfile` (FFF/FFF).
- **Run-to-run spread:** per-rep pass `pu` 25, 26, 26; `jcode` 27, 29, 28. Flaky tasks: `pu` 6, `jcode` 2. Median per-task wall CV: `pu` 0.23, `jcode` 0.17.
- **False completions (agent said completed, acceptance red):** `pu` **12/90 runs**, `jcode` **6/90 runs**. One more `pu` run ended without completing (17 rep 1).
- **Per category (2/3 pass, `pu` / `jcode`):** bugfix 9/9, feature_refactor 9/9, test_build 4/5, no_change 3/3, large_file 2/2.
- **Median turns:** `pu` **16**, `jcode` **19**.
- **Turn breakdown vs v2:** v3 has 90 sessions and 1,606 turns, median 16/session (v2 control: 16.5). Per-session v2 -> v3:
  - shell_read 4.93 -> 5.73
  - read 2.53 -> 2.76
  - edit 2.43 -> 2.68
  - git_history 3.03 -> 2.02 (no future commits to read now)
  - full_suite 1.43 -> 1.38
  - test_before_edit 1.37 -> 0.97
  - test_after_edit 1.00 -> 0.59
  - final 1.03 -> 1.02
  - continuation 0.07 -> 0.06
  - Verification turns fell from 3.8 to 2.9 per session, as intended. Exploration (shell_read + read + git_history + search) is still about 11 turns/session and is the main cost.
- **Secondary (all runs / passed runs):** `pu` **122.1s, $0.01317** per pass; `jcode` **142.0s, $0.01350** per pass (`pu` 14% less time, 2% less cost).
- **Read:** `pu` is now clearly faster and cheaper on the work both tools finish. What blocks GO is reliability: twice `jcode`'s false-completion rate and 3x the flaky tasks. The next lever is completion correctness on multi-file/feature tasks, not fewer turns.

## Phase 0: contamination scan (measurement hygiene)

- **Scanner:** `eval/contamination.py` (offline, over saved session journals) and `eval/contamination.go` (built into `pu-eval`; every new result gets `contamination` + `contamination_evidence`; `report.go` and `analyze_repeat.py` exclude non-clean runs from all metrics and verdicts). Labels:
  - `history_leak`: a tool output shows a commit that isn't an ancestor of the task's start commit (e.g. `git log --all` or `git show` of the fix).
  - `network_leak`: the agent fetched upstream. Signals are `git ls-remote`-style `<sha> HEAD` lines for later commits, `Cloning into`, `From https://`, `go: downloading`, `get https://proxy.golang.org/...`, github `commit/pull/compare` URLs, raw/codeload URLs, or curl output. A `GOPROXY=` env value doesn't count.
  - Test: `TestScanContamination`.
- **Results (`eval/contamination.jsonl`):**
  - **v3 `pu`:** 63/90 clean, 24 network_leak (mostly `git ls-remote`/`go list -m` against GitHub, PR pages), 3 history_leak.
  - **v2 `pu`** (the 94eca17 rerun, shared-worktree harness): 11/30 clean, 15 history_leak, 4 network_leak.
  - **jcode v1/v2/v3 and `pu` v1/v2 originals:** transcripts were deleted with their temp dirs (jcode's isolated home; `pu` worktrees before `--sessions-dir` existed), so they **can't be scanned**. **v1 and v2 results (both tools) are labelled contaminated/unverifiable.** v1/v2 used shared worktrees where `git log --all` showed the fix, and the v2 rerun shows `pu` reading it in half the sessions. Treat the "Pilot v1", "Pilot v2" and "v2 repeat" verdicts as **contaminated**.
- **v3 recomputed on clean runs (`pu` filtered; `jcode` unscannable, so all its runs kept):**
  - Tasks with 3 clean runs for both tools: **14/30**, so the verdict is **INCOMPLETE** by the pre-registered >=25 rule.
  - On those 14: `pu` 11/14 vs `jcode` 13/14 (2/3 pass); wall ratio 0.54, cost ratio 0.53; false completions `pu` 9/42 runs, `jcode` 4/42.
  - The `pu` false-completion rate on clean runs (9/42 = 21%) is **higher** than on all runs (12/90 = 13%). Contamination was helping `pu`, not hiding a reliability edge.
  - This filter is asymmetric because `jcode` couldn't be checked. From now on the harness scans both tools.
- **Network blocking:** not done. `jcode` needs network for the LLM API, so a per-process network namespace would also need an API-only egress proxy. That's out of the 15-minute budget, so detection plus exclusion is the guard for now.

## Phase 1: false-completion diagnosis (v3, dev set)

Sources: `pu` session journals (`--sessions-dir`), each task's hidden acceptance test (`git show <fix> -- <test>`), and the fix commit's file list. All 12 `pu` false completions (FC) are **clean** runs (no leak).

| Task (rep) | `pu` claimed evidence (last green checks) | What acceptance checks that `pu`'s evidence didn't | Fix touched, `pu` didn't |
|---|---|---|---|
| 02_afero_unionfile (1,2,3) | `go vet . && go test ./...` + own `TestUnionFileCloseReturnsNil` | `TestUnionFileClose`: full matrix of base/layer close errors, `BADFD` on empty, close **order** | none (same files) |
| 11_afero_zipfs (1) | `go vet ./... && go test ./...` (existing tests only, no new test) | `TestZipFsNoDirEntry`: zip without directory entries, implicit dirs in Walk/Readdir | `zipfs/testdata/no_dir_entry.zip`, `zipfs_test.go` |
| 13_cast_indirection (1) | ad-hoc `TestReproMatrix` (logs only, no assertions) + `git diff` | `TestIndirect`: `**string`, typed nil in `any`, `ok` flag per case; `basic_test`/`time_test` cases | `basic_test.go`, `time_test.go` |
| 25_cli_root_completion (1,2,3) | own `TestRootCommandCompletion...` shown failing at HEAD and passing after (manual baseline), `go test ./...` | `TestCompletionUsesRunArguments`: **subcommand** completion via a custom `ShellComplete` that passes a parent-less `Command` (needs `cmd.Root().Writer` path) | none in rep 1-2; rep 3 also edited `command.go` |
| 01_afero_regexpfs (2) | `go test ./ -run 'Regexp\|Filter'`, `go test ./...`; own test in `ro_regexp_test.go` | `TestRegexpFsOpenFile`: create/excl/existing/dir/unfiltered matrix on MemMapFs **and OsFs** | `regexpfs_test.go` (new file) |
| 14_cli_completion_name (2,3) | ad-hoc demo test (prints output) / `go test -run TestCompletion .`, `go test ./...` | setup instructions must use the **renamed** command (`source <(foo junky bash)`, fish path), no `foo completion` | none (same files) |
| 15_cli_flag_visit (3) | manual pre/post copy check of own `TestExtFlagVisitedAsSet`, gofmt | `TestExtFlagVisit`: `flag.Visit` reports only the ext flag actually set (plus int/uint flag tests) | `flag_int_test.go`, `flag_uint_test.go` |

- **Causes (12 `pu` FC runs):**
  - **A. Requirement under-covered (11/12):** `pu` changed the right files and its own checks were green, often including a test it showed failing before. But that test exercised a narrower case than the requirement (only the happy path, one backend, the root command instead of subcommands, or no assertions at all).
  - **B. No behaviour test at all (1/12):** 11_zipfs, existing suite only.
  - **Untested packages/importers: 0/12.** In every FC the fix's non-test files were in packages `pu` had already run `go test` on (usually `./...`). So "run tests of touched packages + importers before completing" **would not have caught any of them**.
  - **The evidence gate never fired:** 0/12 FC runs (and 86/90 v3 runs overall) created a plan, and the M4/evidence rule only applies to plan steps. So `pu` completed with no covering evidence in all 12.
- **`jcode` FC (6/90, transcripts not kept, so classified from the hidden test only):** 02_afero x3 (same matrix test `pu` misses; both tools fail all runs, so the requirement is likely under-specified by the prompt), 11_afero_zipfs x2 (same implicit-dir case), 04_cobra_one_required x1 (completion cases in `completions_test.go`). Same cause A. **Shared with `pu`: 02 and 11 (4 of `pu`'s 12).** `pu`-only FC: 13, 25 x3, 01, 14 x2, 15 = **8 runs on 5 tasks**, all cause A.

## Phase 2: smallest fix attempt (not merged)

- **Choice:** Phase 1 showed the suggested "run tests of touched packages + importers" would catch 0/12, because `pu` had already run `go test ./...` in every FC. The mechanical gap that *was* real: the evidence gate only applied to plan steps, and 0/12 FC runs made a plan. **Change (`p0/implicit-gate`, `13d7e8c`, not merged):** with no plan, if non-test code changed and no current passing check covers it (red -> green in-session, or proven failed-before by the auto baseline), `pu` refuses to complete and nudges up to 2 times to write and run a test for the described behaviour (`TestImplicitGateNudgesWhenNoPlanAndUncoveredChange`). The auto baseline also runs without a plan.
- **Measurement (v3 dev set, 3 runs/task, `pu` only, isolated harness; baseline = v3 `pu` at `e672110`):**

| | `e672110` (v3) | gate `13d7e8c` | `jcode` (v3) |
|---|---|---|---|
| False completions, all runs | 12/90 (13.3%) | **12/90 (13.3%)** | 6/90 |
| False completions, clean runs | 12/63 (19.0%) | 10/55 (18.2%) | n/a |
| Runs passed | 77/90 | 76/90 | 84/90 |
| Tasks passed (2/3) | 27/30 | 26/30 | 28/30 |
| Median turns, tasks passed before and after (26) | 16.5 | **20.0** | |
| Median wall, same 26 tasks | 75s | **119s** | |
| Median per-task wall ratio vs `jcode` | 0.70 | **0.853** | |

  - The gate fired in 55 of 90 sessions, and 48 sessions got baseline-proven coverage. The model complied by writing a test that failed before its change and passed after, but **that test was still narrower than the hidden acceptance test**. 02 and 25 stayed FFF, 14 went from PFF to FFP, 01 from PFP to FPF.
  - Contamination got worse too: 35/90 runs (mostly network: `go list -m`/`ls-remote` while writing tests).
- **Decision at 05:52 (pre-registered bar: FC at least halved AND wall ratio <= 0.85): NOT MET.** FC didn't fall (12 -> 12), and the wall ratio went 0.70 -> 0.853. **Not merged; work left on branch `p0/implicit-gate`. `pu` stays at `e672110`. Phase 3 (v4) not run.**
- **No second attempt:** the dominant cause is semantic. The agent's own test asserts a narrower requirement than the task (missing matrix cases, a second backend, subcommand vs root, renamed-command text), in files it already edits and tests. Every mechanical gate I could build in the time left (package/importer tests, per-hunk revert checks, more nudges) checks *that* a test covers the diff, not *whether it covers the requirement*. The data says that costs speed without lowering FC, so I stopped rather than force it.
- **Open:**
  - (1) Cause A probably needs a requirement-level step, e.g. extracting enumerated cases from the prompt/issue into an explicit checklist that each needs an assertion. That's model behaviour, not a harness gate.
  - (2) Network blocking with an API-only egress proxy, so contamination stops eating samples (39% of runs this session).
  - (3) Keep `jcode` transcripts (now scanned live) so both tools are filtered symmetrically.

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

- **Root cause of Pilot v1 `NO-GO` (73.3% vs 96.7%) — Phase 2 hard early-completion fired prematurely:** In `internal/core/loop.go` (`1c72639`), hard-terminating the loop after *any* passing verification `exec` whenever `len(planMgr.OpenSteps()) == 0` cut off multi-file `feature_refactor` runs (5/10) and partial-fix `bugfix`/`test_build` runs (`04`, `11`, `12`, `13`, `17`, `21`) as soon as an intermediate `go test` / `cargo test` passed existing tests before all required edits or new tests were written. Fix for v2: remove the hard programmatic kill (or only allow it when an explicit `plan` exists and all steps were marked `done`), and rely on the system prompt guidance instead.
- **Transient 429 burst handling during 4-way concurrency:** 3 `pu` runs hit transient HTTP 429s under 4 concurrent OpenCode Go streams; adding exponential backoff retry on HTTP 429 inside `internal/provider` (while still classifying hard quota exhaustion as `rate_limited`) would prevent mid-run aborts.
- **Next step:** Revert/soften hard early-completion on a new branch, verify on unit tests + `eval/sample_tasks`, and run Pilot v2 to check whether `pu` closes the 73.3% -> 96.7% success gap while retaining its 2.9x speed and 1.86x cost advantage.
