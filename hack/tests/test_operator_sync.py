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
SYNC_FILES = ("helm/templates/crds/test.yaml", "helm/templates/rbac/test.yaml",
              "roles/tackle/templates/agentic/test.yaml.j2", "bundle/test.yaml")


class OperatorSyncTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.env = dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1",
                        GIT_AUTHOR_NAME="Test Author", GIT_AUTHOR_EMAIL="test@example.com",
                        GIT_COMMITTER_NAME="Test Author", GIT_COMMITTER_EMAIL="test@example.com",
                        BASE="main", HEAD=SYNC_BRANCH, SRC_SHA="a" * 40,
                        GITHUB_OUTPUT=str(self.root / "output"))
        self.remote = self.root / "remote.git"
        self.git(self.root, "init", "--bare", "--initial-branch=main", str(self.remote))
        self.operator = self.clone("operator")
        for name in SYNC_FILES:
            self.stage(self.operator, str(Path(name).parent / ".gitkeep"), "")
        self.commit(self.operator, "shared.txt", "initial\n")
        self.git(self.operator, "push", "origin", "main")
        self.step("Prepare operator sync branch", self.operator)
        self.assertTrue(self.render(self.operator, "first render\n"))
        self.step("Push sync commit", self.operator)

    def git(self, checkout, *args):
        result = subprocess.run(["git", *args], cwd=checkout, env=self.env,
                                text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        self.assertEqual(result.returncode, 0, f"git {' '.join(args)}\n{result.stdout}")
        return result.stdout.strip()

    def clone(self, name):
        checkout = self.root / name
        self.git(self.root, "clone", str(self.remote), str(checkout))
        return checkout

    def stage(self, checkout, name, contents):
        (checkout / name).parent.mkdir(parents=True, exist_ok=True)
        (checkout / name).write_text(contents)
        self.git(checkout, "add", name)

    def render(self, checkout, contents):
        for name in SYNC_FILES:
            (checkout / name).write_text(contents)
        output = Path(self.env["GITHUB_OUTPUT"])
        output.write_text("")
        self.step("Stage and detect config changes", checkout)
        return output.read_text().strip() == "changed=true"

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
        self.assertTrue(self.render(sync, "second render\n"))
        self.step("Push sync commit", sync)

        self.assertEqual((sync / "integration.txt").read_text(), "manual integration\n")
        self.assertEqual((sync / "base.txt").read_text(), "operator update\n")
        for commit in (manual, base_commit):
            self.git(self.remote, "merge-base", "--is-ancestor", commit, SYNC_BRANCH)

    def test_no_manifest_change_leaves_base_merge_local(self):
        previous = self.git(self.remote, "rev-parse", SYNC_BRANCH)
        base = self.clone("base")
        base_commit = self.commit(base, "base.txt", "operator update\n")
        self.git(base, "push", "origin", "main")

        sync = self.clone("sync")
        self.step("Prepare operator sync branch", sync)
        self.git(sync, "merge-base", "--is-ancestor", base_commit, "HEAD")
        self.assertFalse(self.render(sync, "first render\n"))
        self.assertEqual(self.git(self.remote, "rev-parse", SYNC_BRANCH), previous)

    def test_concurrent_push_preserves_remote_commit(self):
        sync = self.clone("sync")
        self.step("Prepare operator sync branch", sync)
        manual = self.commit(self.operator, "integration.txt", "concurrent fix\n")
        self.git(self.operator, "push", "origin", SYNC_BRANCH)

        self.assertTrue(self.render(sync, "second render\n"))
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
