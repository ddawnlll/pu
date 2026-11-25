#!/usr/bin/env python3
"""Pilot v2 analysis: success, paired ratios, false completions, per-category."""
import collections, json, statistics, sys

rows = {}
rl = collections.Counter()
for l in open(sys.argv[1]):
    r = json.loads(l)
    if r.get("rate_limited") or r.get("setup_error"):
        rl[r["tool"]] += 1
        continue
    rows[(r["task_id"], r["tool"])] = r  # last valid row wins
tasks = sorted({t for t, _ in rows})
paired = [t for t in tasks if (t, "pu") in rows and (t, "jcode") in rows]
print(f"paired={len(paired)} rate_limited/setup={dict(rl)}")
for tool in ("pu", "jcode"):
    rs = [rows[(t, tool)] for t in paired]
    p = sum(r["passed"] for r in rs)
    fc = [r["task_id"] for r in rs if not r["passed"] and r.get("agent_status") == "completed"]
    to = sum(bool(r.get("timed_out")) for r in rs)
    print(f"{tool}: pass {p}/{len(rs)} ({100*p/len(rs):.1f}%) timeouts={to} false_completions={len(fc)} {fc}")
both = [t for t in paired if rows[(t, "pu")]["passed"] and rows[(t, "jcode")]["passed"]]
wr = [rows[(t, "pu")]["wall_time_ms"] / rows[(t, "jcode")]["wall_time_ms"] for t in both]
cr = [rows[(t, "pu")]["notional_cost_usd"] / rows[(t, "jcode")]["notional_cost_usd"] for t in both if rows[(t, "jcode")]["notional_cost_usd"]]
print(f"both passed={len(both)} median pu/jcode wall={statistics.median(wr):.3f} cost={statistics.median(cr):.3f}")
print(f"pu wins wall {sum(x<1 for x in wr)}/{len(wr)} cost {sum(x<1 for x in cr)}/{len(cr)} both {sum(a<1 and b<1 for a,b in zip(wr,cr))}")
cats = collections.defaultdict(list)
for t in paired:
    cats[rows[(t, "pu")]["category"]].append(t)
for c, ts in cats.items():
    s = []
    for tool in ("pu", "jcode"):
        ok = [rows[(t, tool)] for t in ts if rows[(t, tool)]["passed"]]
        fc = sum(1 for t in ts if not rows[(t, tool)]["passed"] and rows[(t, tool)].get("agent_status") == "completed")
        mw = statistics.median([r["wall_time_ms"] for r in ok]) / 1000 if ok else 0
        mc = statistics.median([r["notional_cost_usd"] for r in ok]) if ok else 0
        s.append(f"{tool} {len(ok)}/{len(ts)} fc={fc} wall={mw:.1f}s cost=${mc:.4f}")
    print(f"{c} ({len(ts)}): " + " | ".join(s))
for t in paired:
    a, b = rows[(t, "pu")], rows[(t, "jcode")]
    print(t, "pu", a["passed"], a.get("agent_status"), a["turns"], a["wall_time_ms"], "| jc", b["passed"], b.get("agent_status"), b["wall_time_ms"])
