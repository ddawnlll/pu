#!/usr/bin/env python3
"""Mechanical harvester for pilot v2 held-out tasks (same rule as eval/pilot_tasks/SELECTION.md).

For each repo: scan non-merge commits touching >=1 source and >=1 test file (1-12 files),
skip commits used in v1, verify acceptance FAILS at <sha>^ and PASSES at <sha>
(test files from <sha> checked out on top). Emits candidates.jsonl.
"""
import json, os, re, subprocess, sys, tempfile

REPOS = sys.argv[1].split(",")
OUT = sys.argv[2]
EXCLUDE = set(open(sys.argv[3]).read().split())
PER_REPO_SCAN = int(os.environ.get("SCAN", "60"))
ROOT = "/home/quant/src/pilot_repos"
TARGET = "/home/quant/src/pilot_repos/.cargo-target"


def sh(cmd, cwd, timeout=180):
    try:
        p = subprocess.run(cmd, cwd=cwd, shell=True, capture_output=True, text=True, timeout=timeout)
        return p.returncode, p.stdout + p.stderr
    except subprocess.TimeoutExpired:
        return 124, "timeout"


def is_test(f, rust):
    return f.endswith("_test.go") or (rust and (f.startswith("tests/") and f.endswith(".rs")))


def is_src(f, rust):
    return (f.endswith(".go") and not f.endswith("_test.go")) or (rust and f.startswith("src/") and f.endswith(".rs"))


def accept_cmd(sha, tests, rust):
    co = f"git checkout {sha[:8]} -- " + " ".join(tests)
    if rust:
        names = sorted({os.path.splitext(os.path.basename(t))[0] for t in tests})
        return co + " && CARGO_TARGET_DIR=" + TARGET + " cargo test " + " ".join(f"--test {n}" for n in names)
    pkgs = sorted({"./" + (os.path.dirname(t) or ".") for t in tests})
    pkgs = [p if p != "./." else "." for p in pkgs]
    return co + " && go test " + " ".join(pkgs)


out = open(OUT, "a")
for repo in REPOS:
    path = f"{ROOT}/{repo}"
    rust = os.path.exists(f"{path}/Cargo.toml")
    _, log = sh("git log --no-merges --format=%H -n 400", path)
    scanned = 0
    for sha in log.split():
        if scanned >= PER_REPO_SCAN:
            break
        if any(sha.startswith(e) or e.startswith(sha[:7]) for e in EXCLUDE):
            continue
        _, files = sh(f"git show --name-only --format= {sha}", path)
        files = [f for f in files.split() if f]
        if not (1 <= len(files) <= 12):
            continue
        tests = [f for f in files if is_test(f, rust)]
        srcs = [f for f in files if is_src(f, rust)]
        if not tests or not srcs:
            continue
        scanned += 1
        acc = accept_cmd(sha, tests, rust)
        wt = tempfile.mkdtemp(prefix="harv_")
        os.rmdir(wt)
        sh(f"git worktree add -q --detach {wt} {sha}^", path)
        rc_parent, _ = sh(acc, wt)
        sh(f"git checkout -q -f {sha} && git clean -fdq", wt)
        rc_fix, _ = sh(acc, wt)
        maxlines = 0
        for s in srcs:
            try:
                maxlines = max(maxlines, sum(1 for _ in open(os.path.join(wt, s), errors="ignore")))
            except OSError:
                pass
        sh(f"git worktree remove --force {wt}", path)
        _, subj = sh(f"git log -1 --format=%s {sha}", path)
        _, body = sh(f"git log -1 --format=%b {sha}", path)
        rec = dict(repo=repo, sha=sha, subject=subj.strip(), body=body.strip()[:600], files=files, tests=tests,
                   srcs=srcs, max_src_lines=maxlines, acceptance=acc, rc_parent=rc_parent, rc_fix=rc_fix,
                   valid=(rc_parent != 0 and rc_fix == 0))
        out.write(json.dumps(rec) + "\n")
        out.flush()
        print(repo, sha[:8], rc_parent, rc_fix, subj.strip()[:70], flush=True)
