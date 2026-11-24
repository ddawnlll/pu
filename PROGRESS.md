# pu — Progress

Current milestone: **M5 — Eval harness** (M1–M4 merged to `main`)

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

## M5 Checklist — Eval harness

- [ ] `eval/`: task schema (`task.toml`: repo, commit, prompt, acceptance command, category), clean `git worktree` per run, alternating order (odd `jcode -> pu`, even `pu -> jcode`), same model/budget/timeout, append metrics to `results.jsonl`
- [ ] `eval/`: report generator (success rates, median wall time, median notional cost & raw tokens per successful task, spec §12 Go/No-Go verdict)
- [ ] `cmd/pu run`: hidden `--ablate=turn0,hashline,pinned-prefix,staged` switch
- [ ] `eval/`: 3 sample tasks on a small toy repo + dry run producing `results.jsonl` and report (plus jcode adapter checked against `jcode --help` if installed)

### M5 Done-when gates
- [ ] Dry run produces `results.jsonl` and report for `pu` (and `jcode` if installed)
- [ ] `go vet ./...`, `go test ./...`, and `CGO_ENABLED=0` release build pass

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

## Decisions

- **OpenCode Go endpoint path & session header**: Chat-completions models (including pilot model `deepseek-v4.1-flash`) use `https://opencode.ai/inference/go/openai/v1`, `reasoning_content`, and require `x-opencode-session` (`provider.Request.SessionID`).
- **Confirmed pilot model**: `deepseek-v4.1-flash` on OpenCode Go (`openai-chat-compat`) with `reasoning_param = "reasoning_effort"` and `reasoning_effort = "low"`.
- **`reasoning_content` in assistant history on `deepseek-v4.1-flash`**: **Accepted (optional)** (verified live via `TestSmokeTwoTurnToolCallReasoningHistory`). Preserving `ReasoningPart` in assistant history succeeds and retains KV cache hits.
- **Notional list-price cost vs subscription billing**: Local USD cost computed from `models.toml` prices is **notional list-price cost**; every report and journal usage event records **raw token counts** (`input_tokens`, `cached_input_tokens`, `output_tokens`) alongside it.
- **Deterministic tool-call fallback ID**: Empty streamed tool-call IDs receive `call_<turn>_<index>`.
- **Anchored read & revision table location**: 5-letter lowercase ASCII anchors (`[a-z]{5}`, unique per file even for identical lines) and SHA-256 revision tracking live in `internal/edit` so `read` and `patch` share one source of truth.
- **Layer 3 project rules order**: `AGENTS.md`, then `PU.md` (read once at session start, never mutated mid-session).
- **Output spill threshold**: Tool outputs > 64 KiB spill to `.pu/sessions/<id>/out/<call_id>.txt`.

## Open questions

- **M5 pilot pacing vs OpenCode Go 5-hour usage limit**: OpenCode Go enforces a rolling 5-hour allowance (20% of monthly allowance) and weekly limit (50%). The M5 harness should note/handle rate limits cleanly when running the real 30-task pilot.
