"""Offline regression tests; credentials, reviews and Git repositories are synthetic."""
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
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


FAKE_TMUX = r'''#!/bin/sh
state="$FAKE_STATE"
case "$1" in
  has-session) [ -f "$state/started" ] && [ ! -f "$state/killed" ] ;;
  new-session) rm -f "$state/killed"; echo "$@" > "$state/started" ;;
  capture-pane) cat "$state/pane" 2>/dev/null ;;
  kill-session) touch "$state/killed" ;;
  *) exit 0 ;;
esac
'''

FAKE_GIT = r'''#!/bin/sh
echo "$@" >> "$FAKE_STATE/git.log"
case "$*" in
  *"worktree add"*) eval "path=\${$(($# - 1))}"; mkdir -p "$path" ;;
  *"worktree remove"*) eval "path=\${$#}"; rm -rf "$path" ;;
  *"rev-parse"*) echo 0000000000000000000000000000000000000000 ;;
esac
exit 0
'''

TRUST_SCREEN = """Quick safety check: Is this a project you created or one you trust?
Claude Code'll be able to read, edit, and execute files here.
 ❯ No, exit
   Yes, I trust this folder
Enter to confirm · Esc to cancel
"""


class ClaudeTrustTests(unittest.TestCase):
    """Workspace trust for runner-created Claude worktrees (#74); tmux and git are fakes."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        base = Path(self.tmp.name)
        self.base = base
        self.home, self.config_dir, self.wt = base / "home", base / "claude", base / "wt"
        self.runs, self.state, self.bin, self.repo = base / "runs", base / "state", base / "bin", base / "repo"
        for directory in (self.home, self.config_dir, self.wt, self.runs, self.state, self.bin, self.repo):
            directory.mkdir()
        self.config = self.config_dir / ".claude.json"
        self.config.write_text(json.dumps({"numStartups": 3, "projects": {
            "/elsewhere": {"hasTrustDialogAccepted": True, "allowedTools": ["Bash"]}}}))
        for name, body in (("tmux", FAKE_TMUX), ("git", FAKE_GIT),
                           ("claude", "#!/bin/sh\nexit 0\n"), ("codex", "#!/bin/sh\nexit 0\n")):
            (self.bin / name).write_text(body)
            (self.bin / name).chmod(0o755)
        self.prompt = base / "prompt.md"
        self.prompt.write_text("probe\n")
        env = {key: value for key, value in os.environ.items() if key != "KTAGS_SESSION"}
        self.env = {**env, "HOME": str(self.home), "CLAUDE_CONFIG_DIR": str(self.config_dir),
                    "KTAGS_ROOT": str(self.repo), "KTAGS_WORKTREE_DIR": str(self.wt),
                    "KTAGS_RUN_DIR": str(self.runs), "KTAGS_CLAUDE_BIN": str(self.bin / "claude"),
                    "FAKE_STATE": str(self.state), "KTAGS_CLAUDE_LOCK_TRIES": "3",
                    "PATH": str(self.bin) + os.pathsep + os.environ["PATH"]}

    def tearDown(self):
        self.tmp.cleanup()

    def run_script(self, *args, **env):
        return subprocess.run(["bash", str(ROOT / "scripts/session.sh"), *args],
                              capture_output=True, text=True, env={**self.env, **env})

    def trust(self, action, name, **env):
        return subprocess.run(
            ["bash", "-c", 'source "$1"; claude_trust "$2" "$3"', "test",
             str(ROOT / "scripts/session.sh"), action, name],
            capture_output=True, text=True, env={**self.env, **env})

    def projects(self):
        return json.loads(self.config.read_text())["projects"]

    def key(self, name):
        return os.path.realpath(self.wt) + "/" + name

    def test_claude_trust_grants_only_the_new_worktree(self):
        (self.wt / "ktags-74-dev").mkdir()
        result = self.trust("grant", "ktags-74-dev")
        self.assertEqual(result.returncode, 0, result.stderr)
        config = json.loads(self.config.read_text())
        # The key is the realpath Claude falls back to, not the symlinked temp path.
        self.assertEqual(config["projects"][self.key("ktags-74-dev")], {"hasTrustDialogAccepted": True})
        self.assertIs(config["projects"][self.key("ktags-74-dev")]["hasTrustDialogAccepted"], True)
        self.assertEqual(config["projects"]["/elsewhere"], {"hasTrustDialogAccepted": True, "allowedTools": ["Bash"]})
        self.assertEqual(config["numStartups"], 3)
        self.assertEqual(len(config["projects"]), 2)
        self.assertFalse(Path(str(self.config) + ".lock").exists())

    def test_claude_trust_keeps_the_config_mode_and_creates_a_private_one(self):
        (self.wt / "ktags-74-dev").mkdir()
        self.config.chmod(0o640)
        self.assertEqual(self.trust("grant", "ktags-74-dev").returncode, 0)
        self.assertEqual(self.config.stat().st_mode & 0o777, 0o640)
        self.config.unlink()
        self.assertEqual(self.trust("grant", "ktags-74-dev").returncode, 0)
        self.assertEqual(self.config.stat().st_mode & 0o777, 0o600)
        self.assertEqual(json.loads(self.config.read_text()),
                         {"projects": {self.key("ktags-74-dev"): {"hasTrustDialogAccepted": True}}})

    def test_claude_trust_keeps_a_symlinked_config(self):
        real = self.base / "dotfiles.json"
        real.write_text(self.config.read_text())
        self.config.unlink()
        self.config.symlink_to(real)
        (self.wt / "ktags-74-dev").mkdir()
        self.assertEqual(self.trust("grant", "ktags-74-dev").returncode, 0)
        self.assertTrue(self.config.is_symlink())
        self.assertIn(self.key("ktags-74-dev"), json.loads(real.read_text())["projects"])

    def test_claude_trust_refuses_broad_or_foreign_paths(self):
        before = self.config.read_text()
        (self.home / "ktags-74-dev").mkdir()
        (self.wt / "ktags-75-dev").symlink_to(self.home)
        cases = (
            ("grant", "../home", {}),                          # not a session key
            ("revoke", "../home", {}),
            ("grant", "ktags-74-dev", {}),                     # no such worktree
            ("grant", "ktags-75-dev", {}),                     # symlink escaping the root
            ("grant", "ktags-74-dev", {"KTAGS_WORKTREE_DIR": str(self.base / "missing")}),
            ("revoke", "ktags-74-dev", {"KTAGS_WORKTREE_DIR": str(self.base / "missing")}),
            ("grant", "ktags-74-dev", {"KTAGS_WORKTREE_DIR": str(self.home)}),
            ("grant", "ktags-74-dev", {"KTAGS_WORKTREE_DIR": "/"}),
        )
        for action, name, env in cases:
            with self.subTest(action=action, name=name, env=env):
                result = self.trust(action, name, **env)
                self.assertEqual(result.returncode, 60, result.stderr)
                self.assertEqual(self.config.read_text(), before)

    def test_claude_trust_refuses_malformed_config(self):
        (self.wt / "ktags-74-dev").mkdir()
        for body, message in (("{not json", "cannot read"), ("[]", "projects map"),
                              ('{"projects": []}', "projects map"),
                              (json.dumps({"projects": {self.key("ktags-74-dev"): True}}), "projects map")):
            with self.subTest(body=body):
                self.config.write_text(body)
                result = self.trust("grant", "ktags-74-dev")
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertNotIn("Traceback", result.stderr)
                self.assertEqual(self.config.read_text(), body)
                self.assertEqual(sorted(p.name for p in self.config_dir.iterdir()), [".claude.json"])

    def test_claude_trust_waits_for_the_config_lock(self):
        (self.wt / "ktags-74-dev").mkdir()
        before = self.config.read_text()
        lock = Path(str(self.config) + ".lock")
        lock.mkdir()
        result = self.trust("grant", "ktags-74-dev")
        self.assertEqual(result.returncode, 20)
        self.assertIn("lock held", result.stderr)
        self.assertEqual(self.config.read_text(), before)
        self.assertTrue(lock.is_dir(), "a lock held by Claude must not be removed")
        timer = threading.Timer(0.5, lock.rmdir)
        timer.start()
        result = self.trust("grant", "ktags-74-dev", KTAGS_CLAUDE_LOCK_TRIES="50")
        timer.join()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(self.key("ktags-74-dev"), self.projects())
        missing = self.trust("grant", "ktags-74-dev", CLAUDE_CONFIG_DIR=str(self.base / "absent"))
        self.assertEqual(missing.returncode, 20)
        self.assertIn("config directory missing", missing.stderr)

    def test_claude_trust_revoke_removes_only_that_worktree(self):
        for name in ("ktags-74-dev", "ktags-74-review"):
            (self.wt / name).mkdir()
            self.assertEqual(self.trust("grant", name).returncode, 0)
        (self.wt / "ktags-74-dev").rmdir()
        self.assertEqual(self.trust("revoke", "ktags-74-dev").returncode, 0)
        self.assertEqual(set(self.projects()), {"/elsewhere", self.key("ktags-74-review")})
        self.config.unlink()
        self.assertEqual(self.trust("revoke", "ktags-74-review").returncode, 0)
        self.assertFalse(self.config.exists(), "revoke must not create a config")

    def test_runner_pins_the_granted_claude_config(self):
        command = 'source "$1"; claude_config_pin'
        script = str(ROOT / "scripts/session.sh")
        pinned = subprocess.check_output(["bash", "-c", command, "test", script], text=True,
                                         env={**self.env, "CLAUDE_CONFIG_DIR": "/cfg dir"})
        self.assertEqual(pinned.strip(), "export CLAUDE_CONFIG_DIR=/cfg\\ dir")
        env = {key: value for key, value in self.env.items() if key != "CLAUDE_CONFIG_DIR"}
        self.assertEqual(subprocess.check_output(["bash", "-c", command, "test", script],
                                                 text=True, env=env).strip(), "unset CLAUDE_CONFIG_DIR")

    def spawn(self, issue, agent, *extra):
        return self.run_script("spawn", "--issue", str(issue), "--stage", "dev", "--agent", agent,
                               "--prompt-file", str(self.prompt), "--skip-usage-gate", *extra)

    def test_spawn_pretrusts_the_new_claude_worktree(self):
        result = self.spawn(74, "claude")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.projects()[self.key("ktags-74-dev")], {"hasTrustDialogAccepted": True})
        runner = (self.runs / "ktags-74-dev/run.sh").read_text()
        self.assertIn(f"export CLAUDE_CONFIG_DIR={self.config_dir}", runner)

    def test_spawn_aborts_when_trust_cannot_be_granted(self):
        self.config.write_text("{not json")
        result = self.spawn(74, "claude")
        self.assertEqual(result.returncode, 20)
        self.assertIn("cannot pre-trust", result.stderr)
        self.assertIn("NOT started", result.stderr)
        self.assertFalse((self.wt / "ktags-74-dev").exists())
        self.assertIn("worktree remove", (self.state / "git.log").read_text())
        self.assertFalse((self.state / "started").exists(), "nothing may be launched")
        self.assertEqual(self.config.read_text(), "{not json")

    def test_spawn_grants_nothing_for_codex_or_the_checkout(self):
        before = self.config.read_text()
        result = self.spawn(74, "codex")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("CLAUDE_CONFIG_DIR", (self.runs / "ktags-74-dev/run.sh").read_text())
        (self.state / "killed").touch()
        result = self.spawn(75, "claude", "--no-worktree")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.config.read_text(), before)
        self.assertNotIn("CLAUDE_CONFIG_DIR", (self.runs / "ktags-75-dev/run.sh").read_text())

    def make_run(self, key, agent):
        run = self.runs / key
        run.mkdir()
        (run / "meta.json").write_text(json.dumps({"session": key, "agent": agent,
                                                   "cwd": "/w/" + key, "started_epoch": 0}))
        (self.state / "started").touch()
        return run

    def test_wait_reports_trust_screen_as_refused(self):
        run = self.make_run("ktags-74-dev", "claude")
        # The screen appears after the first poll; --interval 90 must not delay the report.
        timer = threading.Timer(1.5, (self.state / "pane").write_text, args=(TRUST_SCREEN,))
        timer.start()
        result = self.run_script("wait", "ktags-74-dev", "--interval", "90", "--timeout", "30",
                                 KTAGS_WAIT_STEP_MAX="1")
        timer.join()
        self.assertEqual(result.returncode, 0, result.stderr)
        outcome = json.loads(result.stdout)
        self.assertEqual(outcome["outcome"], "result")
        self.assertLessEqual(outcome["waited_seconds"], 6)
        report = json.loads((run / "result.json").read_text())
        self.assertEqual(report["status"], "refused")
        self.assertIn("trust screen", report["summary"])
        self.assertIn("/w/ktags-74-dev", report["summary"])
        self.assertTrue((self.state / "killed").exists())

    def test_wait_ignores_quoted_trust_text_and_other_agents(self):
        footer = "\n⏵⏵ bypass permissions on (shift+tab to cycle)\n"
        cases = (("ktags-74-dev", "claude", TRUST_SCREEN + footer),        # child quoting the screen
                 ("ktags-75-dev", "claude", "   Yes, I trust this folder\n"),
                 ("ktags-77-dev", "claude", " ❯ No, exit\n"),
                 ("ktags-76-dev", "codex", TRUST_SCREEN))
        for key, agent, pane in cases:
            with self.subTest(key=key):
                run = self.make_run(key, agent)
                (self.state / "pane").write_text(pane)
                result = self.run_script("wait", key, "--interval", "1", "--timeout", "2")
                self.assertEqual(result.returncode, 62, result.stdout + result.stderr)
                self.assertFalse((run / "result.json").exists())
                self.assertFalse((self.state / "killed").exists())

    def test_clean_revokes_trust_and_notes_a_failure(self):
        for name in ("ktags-74-dev", "ktags-76-dev"):
            (self.wt / name).mkdir()
            (self.runs / name).mkdir()
        self.assertEqual(self.trust("grant", "ktags-74-dev").returncode, 0)
        result = self.run_script("clean", "--issue", "74", "--force", "--purge")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn(self.key("ktags-74-dev"), self.projects())
        self.assertNotIn("not revoked", result.stderr)
        self.config.write_text("{not json")
        result = self.run_script("clean", "--issue", "76", "--force", "--purge")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Claude trust for ktags-76-dev not revoked", result.stderr)
        self.assertFalse((self.wt / "ktags-76-dev").exists())


class GuardInventoryContractTests(unittest.TestCase):
    """#35: the guard inventory must stay in the task skill, the template and the review."""

    def text(self, path):
        # Rules wrap across lines; compare with whitespace collapsed.
        return " ".join((ROOT / path).read_text().split())

    def test_task_skill_requires_inventory_before_ready(self):
        text = self.text(".agents/skills/task/SKILL.md")
        self.assertIn(
            "**Guard inventory.** Before `gh pr ready`, walk `git diff origin/main...HEAD` hunk by "
            "hunk and list **every** new or changed refusal, validation, exclusivity (`O_EXCL`, "
            "`Mkdir` instead of `MkdirAll`, locks, \"at most one\") or error branch.", text)
        self.assertIn("That covers the guard the issue names and every guard you added along the way.", text)
        self.assertIn(
            "Each row in the PR's `Guard inventory` table carries either a break-see-red line "
            "or `not a guard, because …`.", text)
        self.assertIn(
            "Do not say \"ready\" or \"done\" when any of these holds: a criterion has no "
            "evidence; `make check` was not run in this session; a guard has no break-see-red "
            "line; the PR lacks a complete guard inventory (§6); the PR lacks the not-done "
            "section; the tree has uncommitted changes.", text)

    def test_task_skill_requires_a_checkable_reason(self):
        text = self.text(".agents/skills/task/SKILL.md")
        self.assertIn(
            "The reason must be one the reviewer can check against the code: \"unreachable\" "
            "names the caller that prevents it, and \"pure passthrough\" names the tested caller.",
            text)
        self.assertIn("A reason that turns out to be false counts as a missing guard.", text)

    def test_task_skill_gates_ready_on_the_inventory(self):
        text = self.text(".agents/skills/task/SKILL.md")
        self.assertIn(
            "When evidence is complete, including a guard inventory that covers the whole diff "
            "(§6): `gh pr ready`", text)
        self.assertIn(
            "The PR body follows `.github/PULL_REQUEST_TEMPLATE.md`: criteria matrix with "
            "evidence, risk → test table, gate output, the guard inventory, **not done and "
            "why**, and `Closes #$1`.", text)

    def test_template_has_inventory_table(self):
        text = self.text(".github/PULL_REQUEST_TEMPLATE.md")
        self.assertIn("## Guard inventory", text)
        self.assertIn(
            "Every new or changed refusal, validation, exclusivity or error branch in the diff, "
            "not only the one the issue names. One row each; nothing in the diff is left out.", text)
        self.assertIn("Break-see-red, or `not a guard, because …`", text)

    def test_review_checks_inventory_against_diff(self):
        text = self.text(".agents/skills/review/SKILL.md")
        self.assertIn("## 5. Check the guard inventory against the diff", text)
        self.assertIn(
            "Walk the diff yourself and list every new or changed refusal, validation, "
            "exclusivity or error branch before you read the PR's `Guard inventory` table.", text)
        self.assertIn("A guard in the diff with no row in the table is a **High** finding.", text)

    def test_review_makes_a_row_without_evidence_high(self):
        self.assertIn(
            "A row without a break-see-red line or a `not a guard, because …` reason is a "
            "**High** finding.", self.text(".agents/skills/review/SKILL.md"))

    def test_review_makes_a_false_reason_high(self):
        self.assertIn(
            "Check at least one `not a guard` reason against the code. A reason that is false "
            "(for example, the branch is reachable) is a **High** finding.",
            self.text(".agents/skills/review/SKILL.md"))

    def test_workflow_states_the_rule(self):
        text = self.text("docs/workflow.md")
        self.assertIn(
            "**Guard inventory.** Before the PR is marked ready, it lists every new or changed "
            "refusal, validation, exclusivity or error branch in the diff, each with a "
            "break-see-red line or a checkable `not a guard, because …`.", text)
        self.assertIn("The review compares the list with the diff, and a missing guard is High.", text)

    def test_workflow_names_the_cause(self):
        text = self.text("docs/workflow.md")
        self.assertIn(
            "Cause (#35): reviews of PR #32 and PR #34 found guards without tests (`strictInt` "
            "sign refusal, exclusive run directory, meta ID check, and five more in round 2), and "
            "each cost a fix round.", text)
        self.assertIn("Round 3 of #34 then found one false \"not a guard\" reason and one missing row.", text)


class ReviewFollowUpContractTests(unittest.TestCase):
    """#62: rule text from review follow-ups on PR #38, #42 and #60 stays in place."""

    def text(self, path):
        # Rules wrap across lines; compare with whitespace collapsed.
        return " ".join((ROOT / path).read_text().split())

    def test_review_compares_the_two_lists(self):
        self.assertIn(
            "before you read the PR's `Guard inventory` table. Then compare the two lists: "
            "- A guard in the diff with no row in the table",
            self.text(".agents/skills/review/SKILL.md"))

    def test_template_inventory_columns(self):
        lines = (ROOT / ".github/PULL_REQUEST_TEMPLATE.md").read_text().splitlines()
        self.assertIn(
            "| # | Guard / branch | `file:line` | Break-see-red, or `not a guard, because …` |", lines)

    def test_ui_missed_row_matches_cli(self):
        text = self.text("docs/guides/ui.md")
        # #68 D1: the count resets on a fresh measurement and shows only while stale.
        self.assertIn(
            "| missed | a scheduled run that did not happen (laptop asleep, service stopped). The "
            "scheduler never runs it late (`internal/core/schedule`); ADR-0001 requires that it is "
            "never represented as completed.", text)
        self.assertIn(
            "A stale check shows its last result greyed with its age plus the word \"missed\" and "
            "the count of slots missed since its last measurement (CLI \"stale, N missed\"). A "
            "fresh measurement resets the count, so a fresh check shows no missed count; past "
            "slots stay in the run history.", text)
        self.assertNotIn("fresh, N missed", text)
        self.assertIn("A missed run is never shown as ok or as completed |", text)
        # The guide follows the CLI on main; these unit cases pin both wordings there.
        cells = (ROOT / "internal/cli/fleet_test.go").read_text()
        self.assertIn('freshness(service.FleetEntry{Stale: true, Missed: 3}), "stale, 3 missed"', cells)
        self.assertIn('freshness(service.FleetEntry{Missed: 3}), "fresh"', cells)

    def test_development_exit_two_covers_conflict(self):
        self.assertIn(
            "`2` confirmation refused, customer locked, or a service `conflict` (the request does "
            "not fit the service's state: stop with active runs, cancel of a finished run)",
            self.text("docs/guides/development.md"))

    def test_roster_travel_left_to_roster_decision(self):
        self.assertIn(
            "The export below carries the customer directory; how the roster travels with it "
            "(the whole roster or only that customer's recipients) is set by the roster decision "
            "that ADR-0004 names, not by this guide.", self.text("docs/guides/security.md"))
        self.assertIn(
            "operator roster in the config dir, exported with the customer (how: `security.md §2`)",
            self.text("docs/guides/ansible.md"))

    @staticmethod
    def long_prose_lines(text, width=100):
        # Table rows and code blocks are exempt from the width by convention.
        long, fenced = [], False
        for number, line in enumerate(text.splitlines(), 1):
            if line.startswith("```"):
                fenced = not fenced
                continue
            if fenced or line.startswith("|"):
                continue
            if len(line) > width:
                long.append(number)
        return long

    def test_security_paragraph_lines_fit(self):
        self.assertEqual(self.long_prose_lines((ROOT / "docs/guides/security.md").read_text()), [])

    def test_width_check_exempts_only_tables_and_code(self):
        text = "\n".join(["| " + "x" * 120, "```", "y" * 120, "```", "w" * 100, "z" * 101])
        self.assertEqual(self.long_prose_lines(text), [6])


if __name__ == "__main__":
    unittest.main()
