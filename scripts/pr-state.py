#!/usr/bin/env python3
"""Read advisory review and maintainer approval for exactly one PR head. Never writes."""
import argparse
import json
import os
import re
import subprocess


def derive(pr, reviews, comments, maintainer):
    head = pr["head"]["sha"]
    advisory = []
    for review in reviews:
        first = (review.get("body") or "").splitlines()[:1]
        match = re.fullmatch(r"Review: (ACCEPT|REJECT) " + re.escape(head), first[0] if first else "")
        trusted = review.get("user", {}).get("login") == maintainer or review.get("author_association") in ("OWNER", "MEMBER", "COLLABORATOR")
        if match and trusted and review.get("state") == "COMMENTED" and review.get("commit_id") == head:
            advisory.append((review.get("submitted_at", ""), review["id"], match[1]))
    latest = max(advisory, default=None)
    decisions = []
    for comment in comments:
        first = (comment.get("body") or "").splitlines()[:1]
        match = re.fullmatch(r"Maintainer: (APPROVE|REVOKE) " + re.escape(head), first[0] if first else "")
        if match and comment.get("user", {}).get("login") == maintainer:
            decisions.append((comment.get("updated_at") or comment["created_at"], comment["id"], match[1]))
    decision = max(decisions, default=None)
    verdict = latest[2].lower() if latest else "none"
    approved = bool(latest and latest[2] == "ACCEPT" and decision and decision[2] == "APPROVE"
                    and decision[0] >= latest[0] and pr["state"] == "open" and not pr.get("draft"))
    return {"head": head, "verdict": verdict, "maintainer_approved": approved}


def api(endpoint, pages=False):
    args = ["gh", "api", endpoint]
    if pages:
        args += ["--paginate", "--slurp"]
    value = json.loads(subprocess.check_output(args, stderr=subprocess.DEVNULL))
    return [item for page in value for item in page] if pages else value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pr", type=int, required=True)
    args = parser.parse_args()
    repo = os.environ.get("KTAGS_REPO", "nesiler/ktags")
    maintainer = os.environ.get("KTAGS_MAINTAINER", "nesiler")
    endpoint = f"repos/{repo}/pulls/{args.pr}"
    pr = api(endpoint)
    reviews = api(endpoint + "/reviews", pages=True)
    comments = api(f"repos/{repo}/issues/{args.pr}/comments", pages=True)
    result = derive(pr, reviews, comments, maintainer)
    if api(endpoint)["head"]["sha"] != result["head"]:
        raise ValueError("head changed during verification")
    print(json.dumps(result))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError):
        raise SystemExit("Cannot verify PR state; no approval inferred.")
