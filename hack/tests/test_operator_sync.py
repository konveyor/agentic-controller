"""Check sync history preservation using the workflow's Bash steps and local Git."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = yaml.safe_load((ROOT / ".github/workflows/sync-operator.yml").read_text())
STEPS = {step["name"]: step["run"] for step in WORKFLOW["jobs"]["sync"]["steps"] if "run" in step}
SYNC_BRANCH = "sync/agentic-controller"


class OperatorSyncTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.env = dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1",
                        GIT_AUTHOR_NAME="Test Author", GIT_AUTHOR_EMAIL="test@example.com",
                        GIT_COMMITTER_NAME="Test Author", GIT_COMMITTER_EMAIL="test@example.com",
                        BASE="main", HEAD=SYNC_BRANCH, SRC_SHA="a" * 40)
        self.remote = self.root / "remote.git"
        self.git(self.root, "init", "--bare", "--initial-branch=main", str(self.remote))
        self.operator = self.clone("operator")
        self.commit(self.operator, "shared.txt", "initial\n")
        self.git(self.operator, "push", "origin", "main")
        self.step("Prepare operator sync branch", self.operator)
        self.stage(self.operator, "manifest.txt", "first render\n")
        self.step("Push sync commit", self.operator)

    def git(self, checkout, *args):
        return subprocess.check_output(["git", *args], cwd=checkout, env=self.env,
                                       text=True, stderr=subprocess.STDOUT).strip()

    def clone(self, name):
        checkout = self.root / name
        self.git(self.root, "clone", str(self.remote), str(checkout))
        return checkout

    def stage(self, checkout, name, contents):
        (checkout / name).write_text(contents)
        self.git(checkout, "add", name)

    def commit(self, checkout, name, contents):
        self.stage(checkout, name, contents)
        self.git(checkout, "commit", "-s", "-m", f"Update {name}")
        return self.git(checkout, "rev-parse", "HEAD")

    def step(self, name, checkout, success=True):
        result = subprocess.run(["bash", "-c", STEPS[name]], cwd=checkout, env=self.env,
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        self.assertEqual(result.returncode == 0, success, result.stdout)

    def test_sync_preserves_manual_and_base_commits(self):
        manual = self.commit(self.operator, "integration.txt", "manual integration\n")
        self.git(self.operator, "push", "origin", SYNC_BRANCH)
        base = self.clone("base")
        base_commit = self.commit(base, "base.txt", "operator update\n")
        self.git(base, "push", "origin", "main")

        sync = self.clone("sync")
        self.step("Prepare operator sync branch", sync)
        self.stage(sync, "manifest.txt", "second render\n")
        self.step("Push sync commit", sync)

        self.assertEqual((sync / "integration.txt").read_text(), "manual integration\n")
        self.assertEqual((sync / "base.txt").read_text(), "operator update\n")
        for commit in (manual, base_commit):
            self.git(self.remote, "merge-base", "--is-ancestor", commit, SYNC_BRANCH)

    def test_concurrent_push_preserves_remote_commit(self):
        sync = self.clone("sync")
        self.step("Prepare operator sync branch", sync)
        manual = self.commit(self.operator, "integration.txt", "concurrent fix\n")
        self.git(self.operator, "push", "origin", SYNC_BRANCH)

        self.stage(sync, "manifest.txt", "second render\n")
        self.step("Push sync commit", sync, success=False)
        self.assertEqual(self.git(self.remote, "rev-parse", SYNC_BRANCH), manual)

    def test_merge_conflict_leaves_remote_unchanged(self):
        manual = self.commit(self.operator, "shared.txt", "integration change\n")
        self.git(self.operator, "push", "origin", SYNC_BRANCH)
        base = self.clone("base")
        self.commit(base, "shared.txt", "conflicting base change\n")
        self.git(base, "push", "origin", "main")

        self.step("Prepare operator sync branch", self.clone("sync"), success=False)
        self.assertEqual(self.git(self.remote, "rev-parse", SYNC_BRANCH), manual)


if __name__ == "__main__":
    unittest.main()
