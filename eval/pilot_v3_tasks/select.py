#!/usr/bin/env python3
r"""Mechanical selection of the 30 pilot v3 tasks (v2 rule + v2 exclusion + PR/issue titles) from harvest candidates.

Rule (fixed before any v2 run):
- Candidates: valid (fails at <sha>^, passes at <sha>), and no SHA appearing anywhere in v1
  (accepted tasks, start commits, or v1 rejected-candidate table).
- Category by fixed precedence:
  large_file  : any edited source file > 1500 lines
  test_build  : subject matches /\b(test|tests|build|lint|vet|warning|deprecat|compile|go ?1\.\d+)\b/i
                (fallback if quota unmet: commit body matches the same regex)
  feature_refactor : >= 2 source files edited
  bugfix      : otherwise
- Quotas: bugfix 10, feature_refactor 10, test_build 5, no_change 3, large_file 2; <= 6 tasks per repo.
- Fill order: categories in order large_file, test_build, feature_refactor, bugfix; within a
  category, round-robin over repos (alphabetical), newest commit first.
- no_change: HEAD of 3 repos, preferring new repos (alphabetical), prompt built from the newest
  unselected valid bugfix in that repo; acceptance = test packages pass && git diff --quiet.
"""
import collections, glob, json, os, re, subprocess, sys

HARV, OUTDIR = sys.argv[1], sys.argv[2]
V1 = re.findall(r"[0-9a-f]{8}", open(os.path.join(os.path.dirname(__file__), "..", "pilot_tasks", "SELECTION.md")).read())
for _f in glob.glob(os.path.join(os.path.dirname(__file__), "..", "pilot_tasks", "*", "task.toml")):
    V1 += re.findall(r"[0-9a-f]{8,40}", open(_f).read())
for _sel in ("pilot_v2_tasks",):
    V1 += re.findall(r"[0-9a-f]{8}", open(os.path.join(os.path.dirname(__file__), "..", _sel, "SELECTION.md")).read())
    for _f in glob.glob(os.path.join(os.path.dirname(__file__), "..", _sel, "*", "task.toml")):
        V1 += re.findall(r"[0-9a-f]{8,40}", open(_f).read())
V1 = {v[:8] for v in V1}
ROOT = "/home/quant/src/pilot_repos"
NEW_REPOS = ["afero", "cast", "cli", "kong", "yaml"]  # not used in v1 or v2
QUOTA = collections.OrderedDict([("large_file", 2), ("test_build", 5), ("feature_refactor", 10), ("bugfix", 10)])
TB = re.compile(r"\b(test|tests|build|lint|vet|warning|warnings|deprecat\w*|compile|go ?1\.\d+)\b", re.I)


def cat(c):
    if c["max_src_lines"] > 1500:
        return "large_file"
    if TB.search(c["subject"]):
        return "test_build"
    if len(c["srcs"]) >= 2:
        return "feature_refactor"
    return "bugfix"


GH = {"cobra": "spf13/cobra", "toml": "BurntSushi/toml", "serde_json": "serde-rs/json", "lipgloss": "charmbracelet/lipgloss",
      "gjson": "tidwall/gjson", "pflag": "spf13/pflag", "sjson": "tidwall/sjson", "mapstructure": "go-viper/mapstructure",
      "afero": "spf13/afero", "cast": "spf13/cast", "cli": "urfave/cli", "kong": "alecthomas/kong", "yaml": "go-yaml/yaml"}
_cache_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "gh_titles.json")
_cache = json.load(open(_cache_path)) if os.path.exists(_cache_path) else {}


def linked_titles(c):
    """Titles of the PR that merged the commit and of issues it references (via gh, cached)."""
    key = c["sha"]
    if key not in _cache:
        titles = []
        repo = GH.get(c["repo"])
        if repo:
            out = subprocess.run(["gh", "api", f"repos/{repo}/commits/{c['sha']}/pulls", "--jq", ".[0] | [.title, (.body // \"\")] | @json"],
                                 capture_output=True, text=True).stdout.strip()
            pr_body = ""
            if out:
                try:
                    t, pr_body = json.loads(out)
                    if t:
                        titles.append(("PR", t))
                except ValueError:
                    pass
            nums = set(re.findall(r"(?:fix(?:es|ed)?|close[sd]?|resolve[sd]?)\s+#(\d+)", c["subject"] + "\n" + c["body"] + "\n" + (pr_body or ""), re.I))
            for n in sorted(nums)[:2]:
                t = subprocess.run(["gh", "api", f"repos/{repo}/issues/{n}", "--jq", ".title"], capture_output=True, text=True).stdout.strip()
                if t:
                    titles.append(("issue", t))
        _cache[key] = titles
        json.dump(_cache, open(_cache_path, "w"), indent=1, sort_keys=True)
    return _cache[key]


def clean_subj(s):
    s = re.sub(r"\s*\(#\d+\)\s*$", "", s or "").strip().rstrip(".")
    return re.sub(r"^(fix|feat|refactor|chore|perf|docs|test)(\([^)]*\))?!?:\s*", "", s, flags=re.I)


def prompt(c):
    subj = clean_subj(c["subject"])
    body = " ".join(l for l in c["body"].splitlines() if l.strip() and not re.match(r"^(Signed-off|Co-authored|Fixes|Closes|\* |- )", l, re.I))
    body = re.split(r"(?<=[.!?])\s", body)[0].strip()[:300] if body else ""
    extra = [f"{k} title: \"{clean_subj(t)}\"" for k, t in linked_titles(c) if clean_subj(t).lower() != subj.lower()]
    srcs = ", ".join(c["srcs"])
    p = f"In this {c['repo']} repository: {subj[0].upper() + subj[1:]}."
    if extra:
        p += " (" + "; ".join(extra) + ")"
    if body:
        p += f" {body}"
    p += f" Make the change in {srcs} and make sure the package's tests pass."
    return p


def prompt_v2(c):
    subj = re.sub(r"\s*\(#\d+\)\s*$", "", c["subject"]).strip().rstrip(".")
    subj = re.sub(r"^(fix|feat|refactor|chore|perf)(\([^)]*\))?!?:\s*", "", subj, flags=re.I)
    body = " ".join(l for l in c["body"].splitlines() if l.strip() and not re.match(r"^(Signed-off|Co-authored|Fixes|Closes|\* |- )", l, re.I))
    body = re.split(r"(?<=[.!?])\s", body)[0].strip()[:300] if body else ""
    srcs = ", ".join(c["srcs"])
    p = f"In this {c['repo']} repository: {subj[0].upper() + subj[1:]}."
    if body:
        p += f" {body}"
    p += f" Make the change in {srcs} and make sure the package's tests pass."
    return p


cands = []
for f in sorted(glob.glob(f"{HARV}/*.jsonl")):
    seen = set()
    for l in open(f):
        c = json.loads(l)
        if c["valid"] and c["sha"] not in seen and c["sha"][:8] not in V1:
            seen.add(c["sha"])
            c["category"] = cat(c)
            cands.append(c)
order = {}
for c in cands:  # harvest order == git log order (newest first)
    order.setdefault(c["repo"], []).append(c)

per_repo = collections.Counter()
picked = []
for catg, q in QUOTA.items():
    pools = {r: [c for c in lst if c["category"] == catg] for r, lst in sorted(order.items())}
    n = 0
    while n < q and any(pools.values()):
        for r in sorted(pools):
            if n >= q:
                break
            while pools[r] and per_repo[r] >= 6:
                pools[r] = []
            if pools[r]:
                c = pools[r].pop(0)
                picked.append(c)
                per_repo[r] += 1
                n += 1
    if n < q and catg == "test_build":
        # Fallback (fixed rule): commit body (not subject) matches the same regex.
        fb = {r: [c for c in lst if c["category"] in ("feature_refactor", "bugfix") and TB.search(c["body"])]
              for r, lst in sorted(order.items())}
        while n < q and any(fb.values()):
            for r in sorted(fb):
                if n >= q:
                    break
                if per_repo[r] >= 6:
                    fb[r] = []
                if fb[r]:
                    c = fb[r].pop(0)
                    c["category"] = "test_build"
                    picked.append(c)
                    per_repo[r] += 1
                    n += 1
    if n < q:
        print(f"WARNING: {catg} only {n}/{q}", file=sys.stderr)

# no_change
nc = []
used = {c["sha"] for c in picked}
for r in NEW_REPOS + sorted(set(order) - set(NEW_REPOS)):
    if len(nc) >= 3 or per_repo[r] >= 6:
        continue
    src = next((c for c in order.get(r, []) if c["sha"] not in used and c["category"] == "bugfix"), None)
    if not src:
        continue
    head = subprocess.check_output(["git", "-C", f"{ROOT}/{r}", "rev-parse", "HEAD"], text=True).strip()
    pk = src["acceptance"].split("&& go test ", 1)[-1] if "go test" in src["acceptance"] else "."
    rc = subprocess.run(f"go test {pk} && git diff --quiet", cwd=f"{ROOT}/{r}", shell=True, capture_output=True).returncode
    if rc != 0:
        continue
    subj = clean_subj(src["subject"])
    pr = [clean_subj(t) for k, t in linked_titles(src) if k == "PR" and clean_subj(t).lower() != subj.lower()]
    if pr:
        subj += f" / {pr[0]}"
    nc.append(dict(repo=r, sha=head, category="no_change", srcs=src["srcs"],
                   acceptance=f"go test {pk} && git diff --quiet", from_commit=src["sha"],
                   prompt=f"A user reports a possible bug in this {r} repository related to: \"{subj}\". Investigate {', '.join(src['srcs'])}; only make changes if a real bug exists."))
    per_repo[r] += 1
    used.add(src["sha"])
picked += nc

catorder = {"bugfix": 0, "feature_refactor": 1, "test_build": 2, "no_change": 3, "large_file": 4}
picked.sort(key=lambda c: (catorder[c["category"]], c["repo"]))
rows = []
for i, c in enumerate(picked, 1):
    slug = re.sub(r"[^a-z0-9]+", "_", (c.get("subject") or "no_change").lower())[:24].strip("_")
    tid = f"{i:02d}_{c['repo']}_{c['category'] if c['category']=='no_change' else slug}"
    d = os.path.join(OUTDIR, tid)
    os.makedirs(d, exist_ok=True)
    commit = c["sha"] if c["category"] == "no_change" else subprocess.check_output(
        ["git", "-C", f"{ROOT}/{c['repo']}", "rev-parse", c["sha"] + "^"], text=True).strip()
    p = c.get("prompt") or prompt(c)
    with open(os.path.join(d, "task.toml"), "w") as f:
        f.write(f'id = "{tid}"\nrepo = "{ROOT}/{c["repo"]}"\ncommit = "{commit}"\ncategory = "{c["category"]}"\n')
        f.write(f"prompt = {json.dumps(p)}\nacceptance = {json.dumps(c['acceptance'])}\n")
    rows.append((i, tid, c["repo"], c["category"], c.get("from_commit", c["sha"])[:8] if c["category"] != "no_change" else "HEAD (from " + c["from_commit"][:8] + ")", c.get("max_src_lines", 0)))
json.dump(rows, open(os.path.join(OUTDIR, "selected.json"), "w"), indent=1)
print(collections.Counter(c["category"] for c in picked), dict(per_repo))
