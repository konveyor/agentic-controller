"""Run the sync workflow's Git steps against disposable local remotes.

Requires Git, Bash, and PyYAML; no GitHub access or credentials are used.
"""

import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = yaml.safe_load((ROOT / ".github/workflows/sync-operator.yml").read_text())
STEPS = {step["name"]: step for step in WORKFLOW["jobs"]["sync"]["steps"]}


class OperatorSyncTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.remote = self.root / "remote.git"
        self.base = "main"
        self.head = "sync/agentic-controller"
        self.clones = 0
        self.env = dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull,
                        GIT_CONFIG_NOSYSTEM="1", GIT_TERMINAL_PROMPT="0",
                        GIT_AUTHOR_NAME="Test Author", GIT_AUTHOR_EMAIL="test@example.com",
                        GIT_COMMITTER_NAME="Test Author", GIT_COMMITTER_EMAIL="test@example.com")
        self.bin = self.root / "bin"
        self.bin.mkdir()
        gh = self.bin / "gh"
        gh.write_text("#!/bin/sh\n"
                      "if [ \"$1\" = pr ] && [ \"$2\" = list ]; then\n"
                      "  printf '%s\\n' '623'\n"
                      "else\n  exit 1\nfi\n")
        gh.chmod(0o755)
        self.env["PATH"] = str(self.bin) + os.pathsep + self.env["PATH"]
        self.git(self.root, "init", "--bare", str(self.remote))
        self.seed = self.root / "seed"
        self.git(self.root, "init", "--initial-branch=main", str(self.seed))
        files = {
            "roles/tackle/defaults/main.yml": "initial defaults\n",
            "helm/templates/crds/example.yaml": "initial manifest\n",
            "helm/templates/rbac/.keep": "",
            "roles/tackle/templates/agentic/.keep": "",
            "bundle/manifests/operator.yaml": 'metadata:\n  annotations:\n    createdAt: "initial"\n',
        }
        for name, contents in files.items():
            self.write(self.seed, name, contents)
        self.git(self.seed, "add", ".")
        self.git(self.seed, "commit", "-s", "-m", "Initial operator")
        self.git(self.seed, "remote", "add", "origin", str(self.remote))
        self.git(self.seed, "push", "origin", "main")
        self.git(self.remote, "symbolic-ref", "HEAD", "refs/heads/main")

    def git(self, checkout, *args, check=True):
        return subprocess.run(["git", *args], cwd=checkout, env=self.env,
                              text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                              check=check)

    def write(self, checkout, name, contents):
        path = checkout / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents)

    def clone(self, branch=None):
        self.clones += 1
        path = self.root / f"clone-{self.clones}"
        self.git(self.root, "clone", "--branch", branch or self.base, str(self.remote), str(path))
        return path

    def commit(self, checkout, name, contents):
        self.write(checkout, name, contents)
        self.git(checkout, "add", name)
        self.git(checkout, "commit", "-s", "-m", f"Update {name}")
        return self.git(checkout, "rev-parse", "HEAD").stdout.strip()

    def remote_tip(self):
        return self.git(self.remote, "rev-parse", f"refs/heads/{self.head}").stdout.strip()

    def step(self, name, checkout, success=True, extra_env=None):
        output = self.root / "output"
        output.write_text("")
        env = dict(self.env, BASE=self.base, HEAD=self.head, SRC_SHA="a" * 40,
                   GITHUB_OUTPUT=str(output))
        env.update(extra_env or {})
        result = subprocess.run(["bash", "-c", STEPS[name]["run"]], cwd=checkout,
                                env=env, text=True, stdout=subprocess.PIPE,
                                stderr=subprocess.STDOUT)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)
        return result, output.read_text()

    def prepare(self):
        checkout = self.clone()
        self.step("Prepare operator sync branch", checkout)
        return checkout

    def render_and_publish(self, checkout, contents):
        self.write(checkout, "helm/templates/crds/example.yaml", contents)
        _, output = self.step("Detect config changes", checkout)
        self.assertIn("changed=true", output)
        self.step("Push branch and open/update PR", checkout)
        return self.remote_tip()

    def assert_ancestor(self, checkout, ancestor, descendant):
        self.git(checkout, "merge-base", "--is-ancestor", ancestor, descendant)

    def test_first_sync_creates_branch_from_main_or_release(self):
        for base in ("main", "release-0.11"):
            with self.subTest(base=base):
                self.base = base
                self.head = "sync/agentic-controller" + ("" if base == "main" else "-" + base)
                if base != "main":
                    self.git(self.seed, "branch", base)
                    self.git(self.seed, "push", "origin", base)
                checkout = self.prepare()
                original = self.git(checkout, "rev-parse", "HEAD").stdout.strip()
                published = self.render_and_publish(checkout, "first render\n")
                self.assertEqual(self.git(checkout, "rev-parse", published + "^").stdout.strip(), original)

    def test_existing_integration_and_new_base_commits_survive(self):
        first = self.prepare()
        original_sync = self.render_and_publish(first, "first render\n")
        human = self.clone(self.head)
        manual = self.commit(human, "roles/tackle/defaults/main.yml", "metrics defaults\n")
        reader = self.commit(human, "helm/templates/rbac/reader.yaml", "reader identity\n")
        self.git(human, "push", "origin", self.head)
        base = self.clone()
        base_commit = self.commit(base, "operator-base.txt", "new operator change\n")
        self.git(base, "push", "origin", self.base)
        checkout = self.prepare()
        self.assertEqual((checkout / "roles/tackle/defaults/main.yml").read_text(), "metrics defaults\n")
        self.assertEqual((checkout / "helm/templates/rbac/reader.yaml").read_text(), "reader identity\n")
        self.assertEqual((checkout / "operator-base.txt").read_text(), "new operator change\n")
        published = self.render_and_publish(checkout, "second render\n")
        for commit in (original_sync, manual, reader, base_commit):
            self.assert_ancestor(checkout, commit, published)
        merges = self.git(checkout, "log", "--merges", "--format=%B").stdout
        self.assertIn("Signed-off-by:", merges)

    def test_noop_and_timestamp_only_render_do_not_change_remote(self):
        original = self.render_and_publish(self.prepare(), "first render\n")
        checkout = self.prepare()
        _, output = self.step("Detect config changes", checkout)
        self.assertIn("changed=false", output)
        self.write(checkout, "bundle/manifests/operator.yaml",
                   'metadata:\n  annotations:\n    createdAt: "later"\n')
        _, output = self.step("Detect config changes", checkout)
        self.assertIn("changed=false", output)
        self.assertEqual(self.remote_tip(), original)

    def test_branch_can_be_reused_after_squash_merge(self):
        original = self.render_and_publish(self.prepare(), "first render\n")
        base = self.clone()
        self.git(base, "merge", "--squash", "origin/" + self.head)
        self.git(base, "commit", "-s", "-m", "Squash previous sync PR")
        self.git(base, "push", "origin", self.base)
        base_commit = self.git(base, "rev-parse", "HEAD").stdout.strip()
        checkout = self.prepare()
        published = self.render_and_publish(checkout, "second render\n")
        self.assert_ancestor(checkout, original, published)
        self.assert_ancestor(checkout, base_commit, published)
        changed = self.git(checkout, "diff", "--name-only", "origin/" + self.base, "HEAD").stdout.splitlines()
        self.assertEqual(changed, ["helm/templates/crds/example.yaml"])

    def test_concurrent_push_is_rejected_without_losing_remote_commit(self):
        self.render_and_publish(self.prepare(), "first render\n")
        checkout = self.prepare()
        human = self.clone(self.head)
        manual = self.commit(human, "roles/tackle/defaults/main.yml", "concurrent fix\n")
        self.git(human, "push", "origin", self.head)
        self.write(checkout, "helm/templates/crds/example.yaml", "second render\n")
        self.step("Detect config changes", checkout)
        result, _ = self.step("Push branch and open/update PR", checkout, success=False)
        self.assertIn("rejected", result.stdout)
        self.assertEqual(self.remote_tip(), manual)

    def test_base_conflict_stops_before_pushing(self):
        self.render_and_publish(self.prepare(), "first render\n")
        human = self.clone(self.head)
        manual = self.commit(human, "roles/tackle/defaults/main.yml", "integration defaults\n")
        self.git(human, "push", "origin", self.head)
        base = self.clone()
        self.commit(base, "roles/tackle/defaults/main.yml", "conflicting base defaults\n")
        self.git(base, "push", "origin", self.base)
        result, _ = self.step("Prepare operator sync branch", self.clone(), success=False)
        self.assertIn("Resolve conflicts on the sync branch", result.stdout)
        self.assertEqual(self.remote_tip(), manual)

    def test_remote_error_is_not_treated_as_missing_branch(self):
        checkout = self.clone()
        fake_bin = self.root / "failed-remote-bin"
        fake_bin.mkdir()
        git = fake_bin / "git"
        git.write_text("#!/bin/sh\nif [ \"$1\" = ls-remote ]; then exit 128; fi\n"
                       + "exec " + shlex.quote(shutil.which("git")) + ' "$@"\n')
        git.chmod(0o755)
        result, _ = self.step("Prepare operator sync branch", checkout, success=False,
                              extra_env={"PATH": str(fake_bin) + os.pathsep + self.env["PATH"]})
        self.assertEqual(result.returncode, 128)
        self.assertIn("Cannot check operator sync branch", result.stdout)
        self.assertNotEqual(self.git(checkout, "show-ref", "--verify", "--quiet",
                                    "refs/heads/" + self.head, check=False).returncode, 0)


if __name__ == "__main__":
    unittest.main()
