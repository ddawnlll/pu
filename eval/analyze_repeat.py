#!/usr/bin/env python3
"""Pre-registered repeat analysis (see PROGRESS.md "v2 repeat: pre-registered rule").

usage: analyze_repeat.py REP1.jsonl REPEAT.jsonl
Rep 1 rows lack a `rep` field and are treated as rep 1.
"""
import collections, json, statistics, sys

runs = collections.defaultdict(list)  # (task, tool) -> [row]
cat = {}
for path, default_rep in ((sys.argv[1], 1), (sys.argv[2], None)):
    try:
        f = open(path)
    except OSError:
        continue
    latest = {}
    for l in f:
        r = json.loads(l)
        if r.get("rate_limited") or r.get("setup_error") or r.get("agent_status") in ("rate_limited", "setup_error"):
            continue
        rep = r.get("rep") or default_rep or 1
        latest[(r["task_id"], r["tool"], rep)] = r
    for (t, tool, rep), r in latest.items():
        runs[(t, tool)].append(r)
        cat[t] = r["category"]

tasks = sorted(cat)
full = [t for t in tasks if len(runs[(t, "pu")]) >= 3 and len(runs[(t, "jcode")]) >= 3]
print(f"tasks with 3 valid runs per tool: {len(full)}/{len(tasks)}")


def passed(t, tool):
    return sum(r["passed"] for r in runs[(t, tool)][:3]) >= 2


def med(t, tool, k):
    return statistics.median(r[k] for r in runs[(t, tool)][:3] if r["passed"])


res = {}
for tool in ("pu", "jcode"):
    p = [t for t in full if passed(t, tool)]
    allr = [r for t in full for r in runs[(t, tool)][:3]]
    npass = sum(r["passed"] for r in allr)
    fc = sum(1 for r in allr if not r["passed"] and r.get("agent_status") == "completed")
    flaky = sum(1 for t in full if 0 < sum(r["passed"] for r in runs[(t, tool)][:3]) < 3)
    # run-to-run spread: per-run pass rate per rep, and median per-task coefficient of variation of wall on passing runs
    reps = collections.defaultdict(list)
    for t in full:
        for r in runs[(t, tool)][:3]:
            reps[r.get("rep") or 1].append(r["passed"])
    cvs = []
    for t in full:
        w = [r["wall_time_ms"] for r in runs[(t, tool)][:3] if r["passed"]]
        if len(w) >= 2:
            cvs.append(statistics.pstdev(w) / statistics.mean(w))
    tw = sum(r["wall_time_ms"] for r in allr) / 1000
    tc = sum(r["notional_cost_usd"] for r in allr)
    turns = statistics.median(r["turns"] for r in allr if r.get("turns"))
    res[tool] = len(p) / len(full) if full else 0
    print(f"{tool}: 2/3-pass {len(p)}/{len(full)} ({100*res[tool]:.1f}%) | runs passed {npass}/{len(allr)} | "
          f"per-rep pass {', '.join(f'r{k}={sum(v)}/{len(v)}' for k, v in sorted(reps.items()))} | flaky tasks {flaky} | "
          f"false completions {fc}/{len(allr)} runs | median wall CV {statistics.median(cvs):.2f} | median turns {turns} | "
          f"secondary: wall/pass {tw/max(npass,1):.1f}s cost/pass ${tc/max(npass,1):.5f}")

both = [t for t in full if passed(t, "pu") and passed(t, "jcode")]
wr = [med(t, "pu", "wall_time_ms") / med(t, "jcode", "wall_time_ms") for t in both]
cr = [med(t, "pu", "notional_cost_usd") / med(t, "jcode", "notional_cost_usd") for t in both]
mw, mc = statistics.median(wr), statistics.median(cr)
print(f"paired both-passed={len(both)} median ratio wall={mw:.3f} cost={mc:.3f} pu faster on {sum(x<1 for x in wr)} cheaper on {sum(x<1 for x in cr)}")
if len(full) < 25:
    v = "INCOMPLETE"
elif res["pu"] >= res["jcode"] and (mw <= 0.85 or mc <= 0.85):
    v = "GO"
else:
    v = "NO-GO"
print("VERDICT:", v)
for c in sorted(set(cat[t] for t in full)):
    ts = [t for t in full if cat[t] == c]
    print(c, len(ts), " ".join(f"{tool}={sum(passed(t, tool) for t in ts)}" for tool in ("pu", "jcode")))
for t in full:
    print(t, *(f"{tool}:{''.join('P' if r['passed'] else ('F' if r.get('agent_status')=='completed' else 'x') for r in runs[(t, tool)][:3])}" for tool in ("pu", "jcode")))
