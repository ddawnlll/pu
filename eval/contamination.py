#!/usr/bin/env python3
"""Contamination scan of pu session journals (tool outputs).

history_leak: tool output shows a commit object that is a descendant of the
  task's start commit (i.e. a later upstream commit), e.g. via git log --all / git show.
network_leak: tool output shows a network fetch of upstream code (git fetch/clone/pull
  from a URL, go get/go mod download of the module at a newer version, curl/wget of
  GitHub/raw/codeload/proxy URLs).
usage: contamination.py TASKS_DIR SESSIONS_DIR SET_LABEL >> eval/contamination.jsonl
Sessions dir layout: <task_id>__rep<N>/sessions/<sid>/events.jsonl (from pu-eval --sessions-dir).
"""
import glob, json, os, re, subprocess, sys, tomllib

TASKS, SESS, LABEL = sys.argv[1], sys.argv[2], sys.argv[3]
NET = re.compile(
    r"(From https?://|Cloning into|remote: (Enumerating|Counting)|"
    r"go: downloading \S+ v|go: (finding|upgraded)|"
    r"github\.com/[^\s/]+/[^\s/]+/(commit|pull|compare)/|raw\.githubusercontent|codeload\.github|get https://proxy\.golang\.org/|"
    r"HTTP/[12](\.\d)? 200|% Total +% Received)", re.M)
SHA = re.compile(r"\b[0-9a-f]{7,40}\b")


def later_commits(repo, start, cache={}):
    key = (repo, start)
    if key not in cache:
        out = subprocess.run(["git", "-C", repo, "rev-list", "--all", f"^{start}"], capture_output=True, text=True).stdout
        cache[key] = set(out.split())
    return cache[key]


tasks = {}
for f in glob.glob(os.path.join(TASKS, "*", "task.toml")):
    t = tomllib.load(open(f, "rb"))
    tasks[t["id"]] = t

for d in sorted(glob.glob(os.path.join(SESS, "*__rep*"))):
    tid, rep = os.path.basename(d).rsplit("__rep", 1)
    t = tasks.get(tid)
    if not t:
        continue
    later = later_commits(t["repo"], t["commit"])
    prefixes = {c[:7] for c in later}
    status, why = "clean", ""
    for f in glob.glob(os.path.join(d, "**", "events.jsonl"), recursive=True):
        for l in open(f):
            try:
                e = json.loads(l)
            except ValueError:
                continue
            if e.get("type") != "tool_result":
                continue
            o = (e.get("data") or {}).get("raw_output", "")
            m = NET.search(o)
            if m and status != "network_leak":
                status, why = "network_leak", m.group(0)[:80]
            if status == "clean":
                for s in SHA.findall(o):
                    if s[:7] in prefixes:
                        line = next((x for x in o.splitlines() if s in x), "")[:100]
                        # ls-remote style lines ("<sha>\tHEAD" / refs/) mean the commit came from the network.
                        kind = "network_leak" if re.search(re.escape(s) + r"\s+(HEAD|refs/)", o) else "history_leak"
                        status, why = kind, f"{s[:8]}: {line}"
                        break
    print(json.dumps({"set": LABEL, "task_id": tid, "tool": "pu", "rep": int(rep), "status": status, "evidence": why}))
