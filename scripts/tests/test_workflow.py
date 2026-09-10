"""Offline regression tests; credentials, reviews and Git repositories are synthetic."""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]


def module(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / (name + ".py"))
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


approval = module("pr-state")
hooks = module("agent-hook")


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name)
        self.git("init", "-q")
        self.git("config", "user.name", "Test Maintainer")
        self.git("config", "user.email", "test@example.invalid")
        (self.repo / ".gitignore").write_bytes((ROOT / ".gitignore").read_bytes())
        self.git("add", ".gitignore")

    def git(self, *args):
        return subprocess.check_output(["git", "-c", "core.hooksPath=/dev/null",
                                        "-c", "commit.gpgsign=false", *args], cwd=self.repo,
                                       stderr=subprocess.DEVNULL).decode().strip()

    def add(self, path, text="fixture"):
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)
        self.git("add", "-f", path)

    def scan(self, *args):
        return subprocess.run(["python3", str(ROOT / "scripts/repo-policy.py"), *args],
                              cwd=self.repo, capture_output=True, text=True)

    def test_excluded_paths_cannot_be_force_added(self):
        for path in ("personal.md", "docs/plan/task.md", "scripts/local/probe.sh",
                     ".env", "config/.env.production", "secrets/store.json",
                     "nested/key.age", ".serena/memories/state.yml", "chat.jsonl"):
            with self.subTest(path=path):
                self.add(path)
                result = self.scan("index")
                self.assertEqual(result.returncode, 2)
                self.git("rm", "--cached", path)

    def test_shared_contracts_and_secrets_package_are_publishable(self):
        for path in ("README.md", "AGENTS.md", "docs/workflow.md", "docs/guides/testing.md",
                     "docs/adr/0001-example.md", ".agents/skills/task/SKILL.md",
                     ".claude/agents/reviewer.md", "internal/secrets/store.go"):
            self.add(path)
        self.assertEqual(self.scan("index").returncode, 0)

    def test_secret_in_ordinary_file_is_blocked_without_echo(self):
        value = "gh" + "p_" + "A" * 36
        self.add("config.txt", value)
        result = self.scan("index")
        self.assertEqual(result.returncode, 2)
        self.assertNotIn(value, result.stdout + result.stderr)

    def test_scans_index_not_unstaged_working_copy(self):
        self.add("config.txt", "gh" + "p_" + "B" * 36)
        (self.repo / "config.txt").write_text("harmless replacement")
        self.assertEqual(self.scan("index").returncode, 2)

    def test_generic_credential_assignment_blocked(self):
        self.add("settings.json", '"api_key": "' + "X" * 32 + '"')
        self.assertEqual(self.scan("index").returncode, 2)

    def test_removed_private_file_still_blocks_unpublished_history(self):
        self.git("commit", "-qm", "chore: base")
        base = self.git("rev-parse", "HEAD")
        self.add("notes.md")
        self.git("commit", "-qm", "docs: local note")
        self.git("rm", "notes.md")
        self.git("commit", "-qm", "docs: remove note")
        self.assertEqual(self.scan("index").returncode, 0)
        self.assertEqual(self.scan("range", base + "..HEAD").returncode, 2)

    def test_unresolvable_range_fails_closed(self):
        self.assertEqual(self.scan("range", "missing..HEAD").returncode, 2)


class ApprovalTests(unittest.TestCase):
    def setUp(self):
        self.sha = "a" * 40
        self.pr = {"head": {"sha": self.sha}, "state": "open", "draft": False}
        self.reviews = [{"id": 1, "body": "Review: ACCEPT " + self.sha,
                         "user": {"login": "owner"}, "state": "COMMENTED",
                         "commit_id": self.sha, "submitted_at": "2026-09-08T10:00:00Z"}]
        self.comments = [{"id": 2, "body": "Maintainer: APPROVE " + self.sha,
                          "user": {"login": "owner"}, "created_at": "2026-09-08T11:00:00Z"}]

    def state(self):
        return approval.derive(self.pr, self.reviews, self.comments, "owner")

    def test_owner_can_approve_own_pr_after_advisory_review(self):
        self.assertTrue(self.state()["maintainer_approved"])

    def test_new_commit_invalidates_review_and_approval(self):
        self.pr["head"]["sha"] = "b" * 40
        self.assertEqual(self.state()["verdict"], "none")
        self.assertFalse(self.state()["maintainer_approved"])

    def test_outsider_cannot_approve(self):
        self.comments[0]["user"]["login"] = "outsider"
        self.assertFalse(self.state()["maintainer_approved"])

    def test_outsider_cannot_supply_advisory_verdict(self):
        self.reviews[0]["user"]["login"] = "outsider"
        self.assertEqual(self.state()["verdict"], "none")

    def test_review_commit_must_match_marker(self):
        self.reviews[0]["commit_id"] = "b" * 40
        self.assertEqual(self.state()["verdict"], "none")

    def test_approval_before_review_is_not_accepted(self):
        self.comments[0]["created_at"] = "2026-09-08T09:00:00Z"
        self.assertFalse(self.state()["maintainer_approved"])

    def test_revocation_wins(self):
        self.comments.append({**self.comments[0], "id": 3,
                              "body": "Maintainer: REVOKE " + self.sha,
                              "created_at": "2026-09-08T12:00:00Z"})
        self.assertFalse(self.state()["maintainer_approved"])

    def test_later_rejection_wins(self):
        self.reviews.append({**self.reviews[0], "id": 4,
                             "body": "Review: REJECT " + self.sha,
                             "submitted_at": "2026-09-08T12:00:00Z"})
        self.assertEqual(self.state()["verdict"], "reject")
        self.assertFalse(self.state()["maintainer_approved"])

    def test_draft_or_closed_pr_cannot_be_approved(self):
        self.pr["draft"] = True
        self.assertFalse(self.state()["maintainer_approved"])
        self.pr.update(draft=False, state="closed")
        self.assertFalse(self.state()["maintainer_approved"])


class HookTests(unittest.TestCase):
    def check_command(self, command):
        with contextlib.redirect_stderr(io.StringIO()):
            return hooks.pre_tool({"tool_name": "Bash", "tool_input": {"command": command}})

    def test_direct_secret_access_blocked(self):
        for command in ("cat .env", "cat config/.env.production", "cat secret.key",
                        "cat ~/.ssh/id_ed25519", "cat secrets/store.json", "cat kubeconfig"):
            with self.subTest(command=command):
                self.assertEqual(self.check_command(command), 2)

    def test_direct_and_force_push_blocked(self):
        for command in ("git push origin main", "git push origin HEAD:main",
                        "git push --force origin HEAD", "git push -f origin HEAD"):
            self.assertEqual(self.check_command(command), 2)

    def test_advisory_review_allowed_native_approval_blocked(self):
        self.assertEqual(self.check_command("gh pr review 12 --comment --body-file local/review.txt"), 0)
        self.assertEqual(self.check_command("gh pr review 12 --approve"), 2)

    def test_regular_development_allowed(self):
        for command in ("make check", "git diff", "git push origin HEAD",
                        "cat internal/secrets/store.go"):
            self.assertEqual(self.check_command(command), 0)

    def test_patch_policy_checks_path_not_documentation_text(self):
        command = "*** Update File: .gitignore\n+.env\n"
        self.assertEqual(hooks.pre_tool({"tool_name": "apply_patch", "tool_input": {"command": command}}), 0)

    def test_stop_hook_blocks_failed_gate_without_leaking_output(self):
        event = io.StringIO(json.dumps({"stop_hook_active": False}))
        with patch("sys.argv", ["agent-hook.py", "stop"]), patch("sys.stdin", event), \
             patch.object(hooks.subprocess, "check_output", return_value=b" M Makefile\n"), \
             patch.object(hooks.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, b"private")), \
             contextlib.redirect_stderr(io.StringIO()) as output:
            self.assertEqual(hooks.main(), 2)
            self.assertNotIn("private", output.getvalue())


class RunnerTests(unittest.TestCase):
    def test_doctor_requires_executable_hooks(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "scripts").mkdir()
            (root / ".githooks").mkdir()
            (root / "bin").mkdir()
            script = root / "scripts/guard.sh"
            script.write_bytes((ROOT / "scripts/guard.sh").read_bytes())
            for name in ("pre-commit", "commit-msg", "pre-push"):
                path = root / ".githooks" / name
                path.write_text("#!/bin/sh\nexit 0\n")
                path.chmod(0o755 if name != "pre-commit" else 0o644)
            for name in ("go", "gh", "git", "golangci-lint", "jq"):
                path = root / "bin" / name
                path.write_text('''#!/bin/sh
case "$*" in
  *"config --get core.hooksPath"*) echo .githooks ;;
  *"rev-parse HEAD"*) exit 1 ;;
esac
exit 0
''')
                path.chmod(0o755)
            env = {**os.environ, "PATH": str(root / "bin") + os.pathsep + os.environ["PATH"]}
            result = subprocess.run(["bash", str(script), "doctor"], env=env, capture_output=True)
            self.assertEqual(result.returncode, 3)
            (root / ".githooks/pre-commit").chmod(0o755)
            result = subprocess.run(["bash", str(script), "doctor"], env=env, capture_output=True)
            self.assertEqual(result.returncode, 0)

    def launch_args(self, provider, executable, model, env):
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; launch=$(launch_command "$2" "$3" "$4" high test); eval "$launch"',
             "test", str(ROOT / "scripts/session.sh"), provider, str(executable), model],
            capture_output=True, text=True, check=True, env=env,
        )
        return json.loads(result.stdout)

    def test_codex_profile_follows_ktags_root(self):
        # Runner children inherit KTAGS_ROOT (the maintainer checkout); the test must not (#26).
        with tempfile.TemporaryDirectory() as tmp:
            executable = Path(tmp) / "fake engine"
            executable.write_text('#!/usr/bin/env python3\nimport json,sys\nprint(json.dumps(sys.argv[1:]))\n')
            executable.chmod(0o755)
            unset = {key: value for key, value in os.environ.items() if key != "KTAGS_ROOT"}
            other = Path(tmp) / "other checkout"
            for env, root in ((unset, ROOT), ({**unset, "KTAGS_ROOT": str(other)}, other)):
                with self.subTest(root=str(root)):
                    args = self.launch_args("codex", executable, "m", env)
                    permission = next(arg for arg in args if arg.startswith("permissions.ktags_coordinator="))
                    self.assertIn(f'"{root / ".git"}"="write"', permission)
                    self.assertIn(f'"{root / "docs"}"="read"', permission)

    def test_provider_launch_arguments_and_shell_quoting(self):
        with tempfile.TemporaryDirectory() as tmp:
            executable = Path(tmp) / "fake engine"
            executable.write_text('#!/usr/bin/env python3\nimport json,sys\nprint(json.dumps(sys.argv[1:]))\n')
            executable.chmod(0o755)
            env = {**os.environ, "KTAGS_ROOT": str(ROOT)}
            for provider in ("claude", "codex"):
                # A model string remains one argument; command substitution must not execute.
                model = "test model $(false) `false`"
                args = self.launch_args(provider, executable, model, env)
                self.assertEqual(args[args.index("--model") + 1], model)
                if provider == "codex":
                    self.assertIn('model_reasoning_effort="high"', args)
                    self.assertEqual(args[args.index("--ask-for-approval") + 1], "never")
                    self.assertIn('default_permissions="ktags_coordinator"', args)
                    self.assertIn("features.network_proxy=true", args)
                    permission = next(arg for arg in args if arg.startswith("permissions.ktags_coordinator="))
                    self.assertIn('extends=":workspace"', permission)
                    self.assertIn('network={ enabled=true', permission)
                    self.assertIn('"**.github.com"="allow"', permission)
                    self.assertIn(f'"{ROOT / ".git"}"="write"', permission)
                    self.assertNotIn("danger-full-access", args)
                    self.assertNotIn("--dangerously-skip-permissions", args)
                else:
                    self.assertIn("--remote-control", args)
                    # Auto mode denied gates and commits in an unattended child (#25).
                    self.assertIn("--dangerously-skip-permissions", args)

    def test_codex_launch_omits_model_only_when_caller_has_not_resolved_it(self):
        result = subprocess.check_output(
            ["bash", "-c", 'source "$1"; launch_command codex /fake/codex "" "" test',
             "test", str(ROOT / "scripts/session.sh")], text=True)
        self.assertNotIn("--model", result)

    def test_provider_child_model_defaults_and_overrides(self):
        command = ('source "$1"; printf "%s\\n" "$(default_child_model claude)" '
                   '"$(default_child_model codex)"')
        result = subprocess.check_output(
            ["bash", "-c", command, "test", str(ROOT / "scripts/session.sh")], text=True)
        self.assertEqual(result.splitlines(), ["opus", "gpt-5.6-sol"])
        env = {**os.environ, "KTAGS_CLAUDE_CHILD_MODEL": "custom-claude",
               "KTAGS_CODEX_CHILD_MODEL": "custom-codex"}
        result = subprocess.check_output(
            ["bash", "-c", command, "test", str(ROOT / "scripts/session.sh")],
            text=True, env=env)
        self.assertEqual(result.splitlines(), ["custom-claude", "custom-codex"])

    def test_runner_refuses_ambiguous_provider(self):
        env = {key: value for key, value in os.environ.items()
               if key not in ("KTAGS_AGENT", "KTAGS_DEV_AGENT", "KTAGS_REVIEW_AGENT")}
        result = subprocess.run(
            ["bash", str(ROOT / "scripts/session.sh"), "doctor"],
            text=True, capture_output=True, env=env)
        self.assertEqual(result.returncode, 10)
        self.assertIn("select the coordinator provider", result.stderr)

    def test_verify_same_account_advisory_and_owner_approval(self):
        with tempfile.TemporaryDirectory() as tmp:
            executable = Path(tmp) / "gh"
            executable.write_text('''#!/usr/bin/env python3
import json, os, sys
args=sys.argv[1:]
sha="a"*40
if args[:2]==["issue","view"]:
    value=["stage:review"]
elif args[:2]==["pr","list"]:
    value=[{"number":10,"headRefName":"feat/9-example","state":"OPEN","isDraft":False,"url":"https://example.invalid/pr"}]
elif args[:2]==["pr","checks"]:
    value=[{"name":"ci-required","state":os.environ.get("FAKE_CI","SUCCESS")}]
elif args[0]=="api" and args[1].endswith("/reviews"):
    value=[[{"id":1,"body":"Review: ACCEPT "+sha,"commit_id":sha,"state":"COMMENTED","user":{"login":"nesiler"},"submitted_at":"2026-09-08T10:00:00Z"}]]
elif args[0]=="api" and args[1].endswith("/comments"):
    value=[[{"id":2,"body":"Maintainer: APPROVE "+sha,"user":{"login":"nesiler"},"created_at":"2026-09-08T11:00:00Z"}]]
elif args[0]=="api":
    value={"head":{"sha":sha},"state":"open","draft":False}
else:
    raise SystemExit("unexpected fake command")
print(json.dumps(value))
if args[:2]==["pr","checks"] and os.environ.get("FAKE_CI")=="FAILURE":
    sys.exit(1)
''')
            executable.chmod(0o755)
            env = {**os.environ, "PATH": tmp + os.pathsep + os.environ["PATH"], "KTAGS_ROOT": str(ROOT)}
            for ci, expected in (("SUCCESS", 0), ("FAILURE", 70)):
                env["FAKE_CI"] = ci
                result = subprocess.run(["bash", str(ROOT / "scripts/session.sh"), "verify",
                                         "--issue", "9", "--stage", "review", "--json"],
                                        env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, expected, result.stderr)
                data = json.loads(result.stdout)
                self.assertEqual(data["done"], ci == "SUCCESS")
                self.assertEqual(data["verdict"], "accept")


class GuardInventoryContractTests(unittest.TestCase):
    """#35: the guard inventory must stay in the task skill, the template and the review."""

    def text(self, path):
        return (ROOT / path).read_text()

    def test_task_skill_requires_inventory_before_ready(self):
        text = self.text(".agents/skills/task/SKILL.md")
        self.assertIn("**Guard inventory.** Before `gh pr ready`", text)
        self.assertIn("**every** new or changed refusal, validation, exclusivity", text)
        self.assertIn("`not a guard, because …`", text)
        self.assertIn("the PR lacks a complete guard inventory", text)

    def test_template_has_inventory_table(self):
        text = self.text(".github/PULL_REQUEST_TEMPLATE.md")
        self.assertIn("## Guard inventory", text)
        self.assertIn("Break-see-red, or `not a guard, because …`", text)

    def test_review_checks_inventory_against_diff(self):
        text = self.text(".agents/skills/review/SKILL.md")
        self.assertIn("## 5. Check the guard inventory against the diff", text)
        self.assertIn("no row in the table is a **High** finding", text)
        self.assertIn("A reason that is false", text)

    def test_workflow_names_the_cause(self):
        self.assertIn("Cause (#35)", self.text("docs/workflow.md"))


if __name__ == "__main__":
    unittest.main()
