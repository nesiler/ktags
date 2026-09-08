#!/usr/bin/env python3
"""Shared lifecycle checks. Input and failing command output are never echoed."""
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
SECRET_PATH = re.compile(
    r"(?:^|[/\s\"'])\.env[^/\s\"']*|"
    r"\.(?:key|age|pem|p12|pfx)(?=$|[\s\"'])|"
    r"(?:^|[/\s\"'])(?:\.ssh|\.aws|\.kube)(?:/|$)|"
    r"(?:^|[/\s\"'])(?:kubeconfig[^/\s\"']*|rke2\.yaml|id_rsa|id_ed25519)(?=$|[\s\"'/])"
)


def refuse(reason):
    print(reason, file=sys.stderr)
    return 2


def protected_path(value):
    # Resolve relative paths without opening files; normalisation catches ../ and symlinks.
    path = Path(value).expanduser()
    if not path.is_absolute():
        path = Path.cwd() / path
    resolved = path.resolve()
    if SECRET_PATH.search(str(resolved)):
        return True
    return any(resolved == ROOT / p or ROOT / p in resolved.parents
               for p in ("secrets", "credentials"))


def pre_tool(event):
    args = event.get("tool_input", {})
    if isinstance(args, str):
        args = {"command": args}
    if not isinstance(args, dict):
        return refuse("Repository policy: unsupported tool input.")
    for field in ("file_path", "path", "relative_path", "absolute_path"):
        value = args.get(field)
        if isinstance(value, str) and protected_path(value):
            return refuse("Repository policy: secret paths are not agent inputs.")
    command = args.get("command", args.get("cmd", ""))
    if not isinstance(command, str):
        return 0
    if event.get("tool_name") == "apply_patch":
        paths = re.findall(r"^\*\*\* (?:Add|Update|Delete) File: (.+)$", command, re.M)
        return refuse("Repository policy: secret path in patch.") if any(map(protected_path, paths)) else 0
    if SECRET_PATH.search(command) or re.search(r"(?:^|[\s\"'])\.?/?(?:secrets|credentials)/", command):
        return refuse("Repository policy: direct secret-file access is prohibited.")
    if re.search(r"\bgit\b[^\n;&|]*\bpush\b", command):
        if re.search(r"(?:--force(?:-with-lease)?\b|(?:^|\s)-f\b|(?:^|[\s:])main(?:$|[\s:])|\s\+)", command):
            return refuse("Repository policy: direct main or forced pushes are prohibited.")
    if re.search(r"\bgh\s+pr\s+review\b.*--(?:approve|request-changes)\b", command):
        return refuse("Use a COMMENT review; final approval belongs to the maintainer.")
    if re.search(r"\bgit\b[^\n;&|]*\bcommit\b", command):
        if re.search(r"co-authored-by|generated with|claude-session|🤖", command, re.I):
            return refuse("Repository policy: remove attribution from the commit.")
    return 0


def main():
    try:
        event = json.load(sys.stdin)
    except (ValueError, TypeError):
        return refuse("Repository hook: invalid event; check hook configuration.")
    if not isinstance(event, dict):
        return refuse("Repository hook: expected an event object.")
    mode = sys.argv[1]
    if mode == "pre-tool":
        return pre_tool(event)
    if mode == "start":
        result = subprocess.run([str(ROOT / "scripts/guard.sh"), "doctor", "--quiet"],
                                cwd=ROOT, capture_output=True)
        if result.returncode:
            return refuse("Repository doctor failed. Run make guard and resolve the findings.")
        return 0
    if mode == "stop":
        if event.get("stop_hook_active"):
            print("{}")
            return 0
        changed = subprocess.check_output(
            ["git", "status", "--porcelain", "--untracked-files=all"], cwd=ROOT).decode()
        if changed.strip():
            # Keep all tool output private, including potentially sensitive failing fixtures.
            result = subprocess.run(["make", "check"], cwd=ROOT, capture_output=True)
            if result.returncode:
                return refuse("Repository gate failed. Run make check, fix the failure, and retry.")
        print("{}")
        return 0
    return refuse("Repository hook: unknown mode.")


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, subprocess.CalledProcessError):
        sys.exit(refuse("Repository hook could not complete; check the local environment."))
