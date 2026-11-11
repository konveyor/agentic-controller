"""Offline guard checks for the scripted decision sequence; no browser or model API."""

import json
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

from demo_fixture import (
    PROVENANCE_MARKER,
    RESULT_MARKER,
    STEERING_TEXT,
    DemoFixture,
    commit_command,
    provenance_command,
)


class DemoFixtureTest(unittest.TestCase):
    def setUp(self):
        self.temp = TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.fixture = DemoFixture(
            Path(self.temp.name), "unit-take", "http://fixture/sessions/unit-take"
        )
        self.fixture.viewer_ready.set()

    def start(self):
        _, question = self.fixture.model({"messages": []})
        self.assertEqual(question["function"]["name"], "ask_user")
        messages = [{"role": "tool", "content": "The human answered: staging"}]
        _, provenance = self.fixture.model({"messages": messages})
        self.assertEqual(provenance["function"]["name"], "execute")
        return messages

    def test_requires_real_user_answer_before_provenance_tool(self):
        self.fixture.model({"messages": []})
        with self.assertRaisesRegex(RuntimeError, "trusted user answer"):
            self.fixture.model({"messages": []})

    def test_requires_steering_in_next_model_history(self):
        messages = self.start()
        messages.append(
            {
                "role": "tool",
                "content": PROVENANCE_MARKER
                + json.dumps(
                    {
                        "runtime": "lightspeed-agentic",
                        "acp_processes": [{"module": "lightspeed_agentic.acp"}],
                        "session_source_sha256": "unit-test-data",
                    }
                ),
            }
        )
        with self.assertRaisesRegex(RuntimeError, "canary guidance"):
            self.fixture.model({"messages": messages})

    def test_final_reply_derives_runtime_and_commit_from_tool_results(self):
        messages = self.start()
        runtime = {
            "runtime": "lightspeed-agentic",
            "version": "unit-test-version",
            "deepagents_version": "unit-test-deepagents",
            "module_path": "/test/lightspeed_agentic/__init__.py",
            "session_source_sha256": "a" * 64,
            "acp_processes": [{"module": "lightspeed_agentic.acp"}],
        }
        messages.extend(
            [
                {"role": "tool", "content": PROVENANCE_MARKER + json.dumps(runtime)},
                {"role": "user", "content": STEERING_TEXT},
            ]
        )
        text, action = self.fixture.model({"messages": messages})
        self.assertIn("canary", text)
        self.assertEqual(action["function"]["name"], "execute")
        result = {
            "environment": "staging",
            "rollout": "canary",
            "commit": "b" * 40,
            "runtime": runtime,
        }
        messages.append({"role": "tool", "content": RESULT_MARKER + json.dumps(result)})
        text, action = self.fixture.model({"messages": messages})
        self.assertIsNone(action)
        self.assertIn("unit-test-version", text)
        self.assertIn("b" * 40, text)
        self.assertTrue(self.fixture.steer_seen)
        self.assertEqual(self.fixture.stage, "completed")

    def test_final_success_requires_actual_commit_tool_result(self):
        self.fixture.calls = 3
        self.fixture.environment = "staging"
        with self.assertRaisesRegex(RuntimeError, "actual tool evidence"):
            self.fixture.model({"messages": []})

    def test_generated_tool_programs_compile(self):
        source = provenance_command("http://fixture/sessions/test")
        compile(
            source.split("\n", 1)[1].rsplit("LIGHTSPEED_PROVENANCE", 1)[0],
            "provenance-tool",
            "exec",
        )
        source = commit_command("staging")
        compile(
            source.split("\n", 2)[2].split("LIGHTSPEED_RESULT", 1)[0],
            "commit-tool",
            "exec",
        )


if __name__ == "__main__":
    unittest.main()
