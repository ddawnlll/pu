# P0 Pilot Task Set — Mechanical Selection & Freeze Log

Frozen on branch `eval/pilot-tasks` **before any pilot run**. Do not add, drop, or edit tasks based on pilot results; if a task's acceptance command cannot run at all, mark it invalid with the reason and leave it in place.

## Selection Rule

1. **Repo pool (max 6 tasks per repo):**
   - User repos (`gh repo list ddawnlll`): `ddawnlll/tetik2` (Go, 5 tasks), `ddawnlll/tetik` (Rust, 1 task).
   - Well-known public Go/Rust repos: `spf13/cobra` (Go, 5 tasks), `BurntSushi/toml` (Go, 5 tasks), `serde-rs/json` (Rust, 5 tasks), `charmbracelet/lipgloss` (Go, 5 tasks), `tidwall/gjson` (Go, 4 tasks).
   - Excluded `ddawnlll/pu` to avoid any self-benchmark bias.
2. **Mechanical harvest & verification:**
   - Scanned non-merge commits modifying both source and test files (1–12 files changed).
   - For each candidate commit `<sha>`, checked out `<sha>^` in a clean `git worktree` and verified acceptance **fails** (`rc != 0`), then checked out `<sha>` and verified acceptance **passes** (`rc == 0`). Dropped any candidate that failed either check or timed out.
   - For `no_change` tasks, pinned `HEAD` of the repo where the acceptance test + `git diff --quiet` already passes, and phrased the prompt as a plausible user bug report for behavior already fixed in history.
   - For `large_file` tasks, required the commit to edit a source file > 1,500 lines (`cobra/command.go` 2,072 lines; `serde_json/src/de.rs` 2,714 lines).
3. **Target mix (30 tasks total):**
   - `bugfix`: 10
   - `feature_refactor` (multi-file): 10
   - `test_build` (test/build/lint repairs): 5
   - `no_change`: 3
   - `large_file` (>1,500 lines): 2

## Accepted Tasks (30)

| # | Task ID | Repo | Category | Fix Commit (Start = `commit^`) |
|---|---|---|---|---|
| 01 | `01_lipgloss_table_nil` | `lipgloss` | `bugfix` | `7d3aa334` |
| 02 | `02_lipgloss_tree_nil` | `lipgloss` | `bugfix` | `03215bc1` |
| 03 | `03_lipgloss_alpha_enum` | `lipgloss` | `bugfix` | `2a70d4ee` |
| 04 | `04_lipgloss_tree_offset` | `lipgloss` | `bugfix` | `4da0bdd6` |
| 05 | `05_lipgloss_unset_underline` | `lipgloss` | `bugfix` | `0f0a9848` |
| 06 | `06_gjson_zero_prefix` | `gjson` | `bugfix` | `23a31597` |
| 07 | `07_gjson_uint_20byte` | `gjson` | `bugfix` | `f4220de6` |
| 08 | `08_gjson_uint_overflow` | `gjson` | `bugfix` | `c6b726dc` |
| 09 | `09_serde_json_f32_eq` | `serde_json` | `bugfix` | `d8bbd390` |
| 10 | `10_serde_json_arb_prec` | `serde_json` | `bugfix` | `4db7fd64` |
| 11 | `11_tetik2_recall_lessons` | `tetik2` | `feature_refactor` | `e6e18c66` |
| 12 | `12_tetik2_kasa_ledger` | `tetik2` | `feature_refactor` | `368c9e99` |
| 13 | `13_tetik2_candle_structure` | `tetik2` | `feature_refactor` | `5aba1dea` |
| 14 | `14_tetik2_dossier_levels` | `tetik2` | `feature_refactor` | `5dc9fccf` |
| 15 | `15_tetik2_state_panels` | `tetik2` | `feature_refactor` | `e7a4b27a` |
| 16 | `16_cobra_comp_desc` | `cobra` | `feature_refactor` | `8cb30f9c` |
| 17 | `17_cobra_default_comp` | `cobra` | `feature_refactor` | `24ada7fe` |
| 18 | `18_toml_encode_local_dt` | `toml` | `feature_refactor` | `c6d720d8` |
| 19 | `19_toml_max_depth` | `toml` | `feature_refactor` | `6a671b23` |
| 20 | `20_serde_json_number_trait` | `serde_json` | `feature_refactor` | `8c535a26` |
| 21 | `21_serde_json_f64_warn` | `serde_json` | `test_build` | `577729ef` |
| 22 | `22_tetik_xtask_archive` | `tetik` | `test_build` | `1017d83e` |
| 23 | `23_cobra_os_args_comp` | `cobra` | `test_build` | `746ef071` |
| 24 | `24_toml_float_format` | `toml` | `test_build` | `874dff3e` |
| 25 | `25_toml_large_float_exp` | `toml` | `test_build` | `10fb0a63` |
| 26 | `26_toml_no_change_uint` | `toml` | `no_change` | `HEAD` |
| 27 | `27_cobra_no_change_unique` | `cobra` | `no_change` | `HEAD` |
| 28 | `28_gjson_no_change_path` | `gjson` | `no_change` | `HEAD` |
| 29 | `29_cobra_command_help_ctx` | `cobra` | `large_file` | `1995054b` (`command.go`, 2,072 lines) |
| 30 | `30_serde_json_de_enum_key` | `serde_json` | `large_file` | `2037b634` (`src/de.rs`, 2,714 lines) |

## Rejected Candidates

| Repo | Commit | Candidate Category | Rejection Reason |
|---|---|---|---|
| `cobra` | `ab5cadcc` | `large_file` | Acceptance `-run TestReset` already passed at parent |
| `toml` | `277ecb0a` | `bugfix` | Acceptance `-run TestOverflow` already passed at parent |
| `toml` | `ddcbbe20` | `test_build` | Acceptance `-run TestError` already passed at parent |
| `tetik2` | `d5e179da` | `test_build` | Acceptance already passed at parent |
| `tetik` | `f6135e58` | `test_build` | `cargo test -p console` timed out (>60s) at parent |
| `tetik` | `c0755fd5` | `feature_refactor` | Acceptance already passed at parent |
| `lipgloss` | `4b8c6673` | `feature_refactor` | Acceptance already passed at parent |
| `cobra` | `5bef9d8d` | `large_file` | Verified PASS, omitted due to 6-task repo cap and 2-task `large_file` quota already met |
| `cobra` | `ad460ea8`, `6dec1ae2`, `a97f9fd4` | `bugfix`/`test_build` | Verified PASS, omitted due to 5-task/repo balance |
| `gjson` | `f004fea8`, `a825fa56`, `9378d3bb` | `large_file`/`bugfix` | Verified PASS, omitted due to category quotas already met |
| `toml` | `3d3abc24`, `89aab296`, `9fe9ada4`, `e9544453`, `eb8b9987` | `bugfix`/`feature_refactor` | Verified PASS, omitted due to 5-task/repo cap |
| `serde_json` | `65a38ed6`, `cf16f75d` | `large_file`/`bugfix` | Verified PASS, omitted due to 5-task/repo cap |
