# Pilot v2 Held-out Task Set: Mechanical Selection & Freeze Log

Frozen on branch `eval/pilot-v2-tasks` **before any v2 run**. Do not add, drop, or edit tasks after seeing results. If an acceptance command cannot run at all, mark the task invalid with the reason and leave it in place.

## Selection Rule (same as `eval/pilot_tasks/SELECTION.md`, mechanised)

1. **Repo pool (max 6 tasks per repo):** v1 repos `spf13/cobra`, `BurntSushi/toml`, `serde-rs/json`, `charmbracelet/lipgloss`, `tidwall/gjson`, plus **3 repos v1 didn't use**: `spf13/pflag`, `tidwall/sjson`, `go-viper/mapstructure`. `tetik`/`tetik2` were not used. `ddawnlll/pu` is excluded.
2. **Harvest (`harvest.py`):** non-merge commits (newest first) touching 1-12 files, with at least one source file and one test file. Acceptance = `git checkout <sha> -- <test files> && go test <test pkgs>` (Rust: `cargo test --test <name>`). A candidate is kept only if acceptance **fails** at `<sha>^` and **passes** at `<sha>`. Scanned the 25 newest qualifying commits per repo (60 for cobra/toml/gjson after test_build came up short).
3. **Held out from v1:** any SHA that appears in v1 (accepted fix commits, start commits, and v1's rejected-candidate table) is excluded.
4. **Category (`select.py`, fixed precedence):** `large_file` if an edited source file is >1,500 lines; `test_build` if the subject matches `\b(test|tests|build|lint|vet|warning(s)|deprecat*|compile|go 1.x)\b`, falling back to the commit body if the quota isn't met; `feature_refactor` if 2+ source files are edited; otherwise `bugfix`.
5. **Quotas:** bugfix 10, feature_refactor 10, test_build 5, no_change 3, large_file 2. Fill order is large_file, test_build, feature_refactor, bugfix, round-robin over repos (alphabetical), newest commit first.
6. **no_change:** `HEAD` of the new repos (mapstructure, pflag, sjson). The prompt is a user bug report built from the newest unselected bugfix in that repo (already fixed in history). Acceptance is `go test <pkg> && git diff --quiet`, verified green at HEAD.
7. **Prompts:** generated mechanically from the commit subject, the first body sentence, and the edited source files. They were not hand-tuned, so some are terse (e.g. 16 `Update`); that affects both tools equally.

Start commit = `<fix>^` (no_change: repo HEAD). Full per-candidate rc data is in the harvest logs (not committed).

## Accepted Tasks (30)

| # | Task ID | Repo | Category | Fix Commit | Max src lines |
|---|---|---|---|---|---|
| 01 | `01_cobra_completion_improve_detec` | `cobra` | `bugfix` | `0745e557` | 951 |
| 02 | `02_cobra_don_t_complete_help_flag` | `cobra` | `bugfix` | `b711e876` | 906 |
| 03 | `03_lipgloss_fix_color_expand_rgbcolo` | `lipgloss` | `bugfix` | `e3f05115` | 354 |
| 04 | `04_lipgloss_fix_don_t_mangle_embedde` | `lipgloss` | `bugfix` | `ce49a2aa` | 665 |
| 05 | `05_mapstructure_fix_nil_value_in_decode` | `mapstructure` | `bugfix` | `cd52f897` | 738 |
| 06 | `06_mapstructure_feat_add_back_previous_s` | `mapstructure` | `bugfix` | `17cdcb07` | 714 |
| 07 | `07_pflag_implement_slicevalue_for` | `pflag` | `bugfix` | `fa02ca0a` | 187 |
| 08 | `08_serde_json_parse_uxxxx_escapes_fast` | `serde_json` | `bugfix` | `86d0e114` | 1028 |
| 09 | `09_sjson_fix_deleting_keys_starti` | `sjson` | `bugfix` | `54fa3d47` | 794 |
| 10 | `10_toml_correct_encoding_of_comm` | `toml` | `bugfix` | `d10101f4` | 776 |
| 11 | `11_cobra_add_env_variable_to_supp` | `cobra` | `feature_refactor` | `e63925d3` | 939 |
| 12 | `12_cobra_update_shell_completion` | `cobra` | `feature_refactor` | `5f2ec3c8` | 832 |
| 13 | `13_lipgloss_feat_add_fitcontent_prop` | `lipgloss` | `feature_refactor` | `6a419c65` | 581 |
| 14 | `14_lipgloss_fix_table_height_and_ove` | `lipgloss` | `feature_refactor` | `c289bad5` | 568 |
| 15 | `15_lipgloss_feat_add_hyperlink_suppo` | `lipgloss` | `feature_refactor` | `c95c5f3c` | 900 |
| 16 | `16_mapstructure_refactor_update` | `mapstructure` | `feature_refactor` | `86ed5b59` | 691 |
| 17 | `17_pflag_fix_honor_custom_isboolf` | `pflag` | `feature_refactor` | `ad27a401` | 1406 |
| 18 | `18_pflag_fix_map_ensure_string_to` | `pflag` | `feature_refactor` | `4b232952` | 1301 |
| 19 | `19_toml_add_decoder_maxtablenest` | `toml` | `feature_refactor` | `2e90ced6` | 846 |
| 20 | `20_toml_enable_toml_1_1_by_defau` | `toml` | `feature_refactor` | `52534926` | 1248 |
| 21 | `21_cobra_improve_api_to_get_flag` | `cobra` | `test_build` | `a0a6ae02` | 901 |
| 22 | `22_lipgloss_fix_table_add_tests_fix` | `lipgloss` | `test_build` | `1427d827` | 491 |
| 23 | `23_pflag_fix_report_the_flag_name` | `pflag` | `test_build` | `6f1cdf1e` | 1421 |
| 24 | `24_pflag_fix_honour_deprecated_pa` | `pflag` | `test_build` | `94000ccc` | 1407 |
| 25 | `25_toml_add_some_boring_tests_fo` | `toml` | `test_build` | `b535ff89` | 1268 |
| 26 | `26_mapstructure_no_change` | `mapstructure` | `no_change` | `HEAD (from ed3f9218)` | 0 |
| 27 | `27_pflag_no_change` | `pflag` | `no_change` | `HEAD (from 9c6d78e4)` | 0 |
| 28 | `28_sjson_no_change` | `sjson` | `no_change` | `HEAD (from f0069f8a)` | 0 |
| 29 | `29_cobra_allow_linker_to_perform` | `cobra` | `large_file` | `611e16c3` | 2056 |
| 30 | `30_gjson_ensure_floating_points_a` | `gjson` | `large_file` | `8d89927e` | 3694 |

Per repo: cobra 6, lipgloss 6, pflag 6, mapstructure 4, toml 4, sjson 2, gjson 1, serde_json 1.
