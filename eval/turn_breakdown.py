#!/usr/bin/env python3
"""Turn breakdown from pu session journals (events.jsonl).

Each model turn is put in one bucket, by its tool calls (priority order):
  continuation     - turn right after an open_step_continuation nudge
  edit             - any patch call
  test_after_edit  - exec that ran tests, after the first patch (full_suite if >1 package reported)
  full_suite       - test exec reporting >1 package/crate result after the first edit
  test_before_edit - exec that ran tests before any patch
  git_history      - exec showing git log/show output
  build_lint       - exec with no output beyond the exit line (build, vet, gofmt)
  shell_read       - other exec used to look at code (cat, grep, ls, sed)
  read / search    - read-only exploration
  plan             - plan tool only
  final            - turn with no tool calls (the closing message)
usage: turn_breakdown.py SESSIONS_DIR [SESSIONS_DIR...]
"""
import collections, glob, json, os, re, statistics, sys

TEST = re.compile(r"(^ok  \t|^FAIL|^--- (PASS|FAIL)|^=== RUN|test result:|^PASS$|no test files|panic:)", re.M)


def classify(path):
    turns = collections.defaultdict(list)
    cont_after = set()
    last_turn = 0
    for l in open(path):
        try:
            e = json.loads(l)
        except ValueError:
            continue
        d = e.get("data") or {}
        if e["type"] == "turn_usage":
            last_turn = d.get("turn", last_turn)
            turns.setdefault(last_turn, [])
        elif e["type"] == "tool_result":
            turns[d.get("turn", last_turn)].append(d)
        elif e["type"] == "open_step_continuation":
            cont_after.add(d.get("turn", last_turn) + 1)
    out = []
    edited = False
    for t in sorted(turns):
        calls = turns[t]
        names = {c["name"] for c in calls}
        execs = [c for c in calls if c["name"] == "exec"]
        tests = [c for c in execs if TEST.search(c.get("raw_output", ""))]
        if t in cont_after and not calls:
            b = "continuation"
        elif "patch" in names:
            b = "edit"
        elif tests:
            pk = max(len(re.findall(r"^(ok  |FAIL|\?   )\s", c.get("raw_output", ""), re.M)) for c in tests)
            crates = max(len(re.findall(r"test result:", c.get("raw_output", ""))) for c in tests)
            if not edited:
                b = "test_before_edit"
            elif pk > 1 or crates > 1:
                b = "full_suite"
            else:
                b = "test_after_edit"
        elif execs:
            outs = "\n".join(c.get("raw_output", "") for c in execs)
            if re.search(r"^commit [0-9a-f]{40}|^[0-9a-f]{7,} ", outs, re.M):
                b = "git_history"
            elif len(outs.splitlines()) <= len(execs) * 2:
                b = "build_lint"  # exit line only: build/vet/gofmt/silent command
            else:
                b = "shell_read"  # cat/grep/ls/sed via exec
        elif "read" in names:
            b = "read"
        elif "search" in names:
            b = "search"
        elif "plan" in names:
            b = "plan"
        elif not calls:
            b = "continuation" if t in cont_after else "final"
        else:
            b = "other"
        if t in cont_after and b != "continuation":
            out.append("continuation")  # count the nudge-induced turn as continuation-driven
        else:
            out.append(b)
        if "patch" in names:
            edited = True
    return out


tot = collections.Counter()
per = []
for root in sys.argv[1:]:
    for f in sorted(glob.glob(os.path.join(root, "**", "events.jsonl"), recursive=True)):
        b = classify(f)
        if not b:
            continue
        per.append(len(b))
        tot.update(b)
n = sum(tot.values())
print(f"sessions={len(per)} turns={n} median_turns/session={statistics.median(per) if per else 0}")
for k, v in tot.most_common():
    print(f"  {k:17s} {v:5d}  {100*v/n:5.1f}%  per-session {v/len(per):.2f}")
