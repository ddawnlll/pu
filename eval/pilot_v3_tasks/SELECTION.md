# Pilot v3 Held-out Task Set: Mechanical Selection & Freeze Log

Frozen on branch `eval/pilot-v3-tasks` **before any v3 run**. Do not add, drop, or edit tasks after seeing results. If an acceptance command can't run at all, mark the task invalid and leave it in place.

## Selection Rule (v2 rule, `select.py`)

1. **Harvest:** `eval/pilot_v2_tasks/harvest.py`, unchanged. A candidate commit touches 1-12 files, including at least one source file and one test file. Its acceptance (`git checkout <sha> -- <tests> && go test <pkgs>`) must fail at `<sha>^` and pass at `<sha>`. Candidates are the v2 pool (25-60 newest qualifying commits per repo) plus the 25 newest from each new repo.
2. **Held out from v1 and v2:** any SHA that appears in v1's or v2's `SELECTION.md` or `task.toml` files (accepted, start, or rejected) is excluded.
3. **New repos (v1/v2 didn't use them):** `spf13/afero`, `spf13/cast`, `urfave/cli`, `alecthomas/kong`. `go-yaml/yaml` was harvested too but produced no valid candidate.
4. **Category and quotas:** same as v2 (large_file >1,500 lines; test_build by subject regex with body fallback; feature_refactor 2+ source files; else bugfix). Quotas 10/10/5/3/2, at most 6 per repo, round-robin over repos, newest first. no_change uses HEAD of new repos.
5. **Prompts:** commit subject + first body sentence + edited source files, plus the **linked PR title** (`gh api repos/<r>/commits/<sha>/pulls`) and the titles of issues it references (`fixes/closes/resolves #N`) when they differ from the subject. Titles are cached in `gh_titles.json`. No prompt is a single word (the minimum is 21 words).

## Accepted Tasks (30)

| # | Task ID | Repo | Category | Fix Commit | Max src lines |
|---|---|---|---|---|---|
| 01 | `01_afero_fix_allow_regexpfs_openf` | `afero` | `bugfix` | `36101d9a` | 230 |
| 02 | `02_afero_return_nil_after_success` | `afero` | `bugfix` | `0e7cde7b` | 337 |
| 03 | `03_cli_fix_count_runes_not_byte` | `cli` | `bugfix` | `ca8f45a9` | 608 |
| 04 | `04_cobra_add_one_required_flag_gr` | `cobra` | `bugfix` | `c81c46a0` | 290 |
| 05 | `05_kong_fix_accept_typed_duratio` | `kong` | `bugfix` | `d1a414be` | 977 |
| 06 | `06_lipgloss_inline_replace_newlines` | `lipgloss` | `bugfix` | `4a8c4de9` | 639 |
| 07 | `07_mapstructure_feat_add_decode_hook_for` | `mapstructure` | `bugfix` | `2e014254` | 654 |
| 08 | `08_pflag_fix_initialize_default_u` | `pflag` | `bugfix` | `18596fac` | 1431 |
| 09 | `09_sjson_limit_array_padding_and` | `sjson` | `bugfix` | `9f1cbfdb` | 794 |
| 10 | `10_toml_mark_meta_keys_as_decode` | `toml` | `bugfix` | `6f7689d2` | 638 |
| 11 | `11_afero_zipfs_support_zip_files` | `afero` | `feature_refactor` | `d4f7abb6` | 176 |
| 12 | `12_cast_fix_float_string_to_numb` | `cast` | `feature_refactor` | `fa4ea64d` | 549 |
| 13 | `13_cast_fix_indirection_of_typed` | `cast` | `feature_refactor` | `1b425f31` | 116 |
| 14 | `14_cli_fix_use_the_configured_c` | `cli` | `feature_refactor` | `c212c5ca` | 319 |
| 15 | `15_cli_fix_record_go_flags_set` | `cli` | `feature_refactor` | `89fb8488` | 318 |
| 16 | `16_kong_add_last_wins_flag_group` | `kong` | `feature_refactor` | `a60008c6` | 1344 |
| 17 | `17_kong_feat_support_negatable_b` | `kong` | `feature_refactor` | `4e565e8d` | 1213 |
| 18 | `18_lipgloss_fix_rename_underline_con` | `lipgloss` | `feature_refactor` | `354e70d6` | 819 |
| 19 | `19_lipgloss_feat_blendlinear_blend` | `lipgloss` | `feature_refactor` | `9a771f5a` | 431 |
| 20 | `20_toml_add_position_col_410` | `toml` | `feature_refactor` | `3cb6f887` | 844 |
| 21 | `21_afero_memmap_add_basic_read_wr` | `afero` | `test_build` | `ecf3363d` | 484 |
| 22 | `22_cast_refactor_improve_slice_t` | `cast` | `test_build` | `c0ba9514` | 106 |
| 23 | `23_cast_refactor_improve_time_te` | `cast` | `test_build` | `cd8c9c35` | 106 |
| 24 | `24_cast_test_add_pointer_tests_t` | `cast` | `test_build` | `a5c9ee71` | 525 |
| 25 | `25_cli_fix_complete_the_argumen` | `cli` | `test_build` | `768449de` | 563 |
| 26 | `26_afero_no_change` | `afero` | `no_change` | `HEAD (from 83e877f7)` | 0 |
| 27 | `27_cli_no_change` | `cli` | `no_change` | `HEAD (from ce39339c)` | 0 |
| 28 | `28_kong_no_change` | `kong` | `no_change` | `HEAD (from af6c6514)` | 0 |
| 29 | `29_cast_add_unsigned_integer_sup` | `cast` | `large_file` | `b7a72fcc` | 1518 |
| 30 | `30_cobra_fix_help_text_for_runnab` | `cobra` | `large_file` | `a73b9c39` | 1891 |

Per repo: cast 6, afero 5, cli 5, kong 4, lipgloss 3, cobra 2, toml 2, mapstructure 1, pflag 1, sjson 1.
