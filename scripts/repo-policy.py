#!/usr/bin/env python3
"""Check publication candidates without printing file contents or secret values."""
import re
import subprocess
import sys

# Signatures, not real credentials. Constructed patterns also avoid self-matches.
SECRET_PATTERNS = [
    rb"-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----",
    rb"AGE-SECRET-KEY-" + rb"[A-Z0-9]{40,}",
    rb"\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b",
    rb"\bsk-(?:proj-|ant-)?[A-Za-z0-9_-]{32,}\b",
    rb"\bAKIA[A-Z0-9]{16}\b",
    rb"\bxox[baprs]-[A-Za-z0-9-]{20,}\b",
    rb"\bAIza[0-9A-Za-z_-]{35}\b",
    rb"(?m)^\s*(?:client-key-data|client-certificate-data):\s*\S+",
    rb"(?im)^\s*(?:export\s+)?[\"']?(?:api[_-]?key|access[_-]?token|client[_-]?secret|password)[\"']?\s*[:=]\s*[\"']?[A-Za-z0-9_/+=.-]{16,}",
]


def git(*args, data=None):
    return subprocess.check_output(["git", *args], input=data, stderr=subprocess.DEVNULL)


def ignored(paths):
    if not paths:
        return set()
    result = subprocess.run(
        ["git", "check-ignore", "--no-index", "-z", "--stdin"],
        input=b"\0".join(paths) + b"\0", stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL, check=False,
    )
    if result.returncode not in (0, 1):
        raise RuntimeError("cannot evaluate publication ignore rules")
    return set(result.stdout.split(b"\0")) - {b""}


def entries(ref):
    if ref is None:
        rows = git("ls-files", "--stage", "-z").split(b"\0")
    else:
        rows = git("ls-tree", "-r", "-z", ref).split(b"\0")
    for row in rows:
        if row:
            meta, path = row.split(b"\t", 1)
            fields = meta.split()
            yield path, fields[1 if ref is None else 2]


def check(refs):
    seen = set()
    failures = 0
    for ref in refs:
        candidates = list(entries(ref))
        denied = ignored([p for p, _ in candidates])
        for path, oid in candidates:
            key = (path, oid)
            if key in seen:
                continue
            seen.add(key)
            label = "index" if ref is None else ref[:12]
            if path in denied:
                # Do not read prohibited blobs, even when they are already in Git.
                print(f"publication: {label}: excluded path {path.decode(errors='replace')!r}", file=sys.stderr)
                failures += 1
                continue
            content = git("cat-file", "blob", oid.decode())
            if any(re.search(pattern, content) for pattern in SECRET_PATTERNS):
                print(f"publication: {label}: secret signature in {path.decode(errors='replace')!r} (value withheld)", file=sys.stderr)
                failures += 1
    return 2 if failures else 0


def main():
    if len(sys.argv) == 2 and sys.argv[1] == "index":
        return check([None])
    if len(sys.argv) == 3 and sys.argv[1] == "range":
        refs = git("rev-list", sys.argv[2]).decode().splitlines()
        return check(refs)
    print("usage: repo-policy.py index | range <revision-range>", file=sys.stderr)
    return 1


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError):
        print("publication: could not verify candidates; refusing publication", file=sys.stderr)
        sys.exit(2)
