#!/usr/bin/env python3
"""Exercise the production Go harness with the real Lightspeed ACP runtime.

Run with the runtime checkout's .venv/bin/python. All model responses and Hub
records are local fixtures; no cluster or real provider credentials are used.
"""

from __future__ import annotations

import argparse
import asyncio
import json
import subprocess
import threading
import time
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def git(*args: str, cwd: Path | None = None) -> str:
    return subprocess.check_output(
        ["git", *args], cwd=cwd, text=True, stderr=subprocess.DEVNULL
    ).strip()


class Fixture:
    def __init__(self, directory: Path, case: str):
        self.directory = directory
        self.case = case
        self.requests: list[dict] = []
        self.hub_requests: list[dict] = []
        self.revoked = False
        self.calls = 0
        self.classifier_calls = 0
        self.viewer_frames: list[dict] = []
        self.continuation = False
        self.answer_seen = False
        self.cancel_sent = False
        self.session_id = None
        self.run_id = None
        self.steer_sent = False
        self.steer_accepted = False
        self.steer_seen = False
        self.reconnected = False
        self.replay_received = False
        self.connections = 0
        self.model_ready = threading.Event()
        self.viewer_ready = threading.Event()
        self.repo = directory / "remote.git"
        seed = directory / "seed"
        seed.mkdir()
        git("init", "-b", "main", cwd=seed)
        git("config", "user.name", "fixture", cwd=seed)
        git("config", "user.email", "fixture@example.com", cwd=seed)
        (seed / "README.md").write_text("Local Lightspeed fixture\n")
        git("add", ".", cwd=seed)
        git("commit", "-m", "Initial fixture", cwd=seed)
        git("clone", "--bare", str(seed), str(self.repo))

    def model(self, request: dict) -> tuple[str, dict | None]:
        messages = request.get("messages", [])
        if any(
            "tool-result safety classifier" in str(m.get("content", ""))
            for m in messages
            if m.get("role") == "system"
        ):
            self.classifier_calls += 1
            return json.dumps({"injectionDetected": False, "category": "none"}), None
        self.calls += 1
        self.steer_seen = self.steer_seen or any(
            "STEERING_CONTEXT_MARKER" in str(m.get("content", ""))
            for m in messages
            if m.get("role") == "user"
        )
        # Let a real viewer attach before the first tool approval is raised.
        if self.calls == 1:
            self.model_ready.set()
            if not self.viewer_ready.wait(getattr(self, "viewer_timeout", 15)):
                raise RuntimeError("viewer failed to attach")
        handoff = any(
            "You have reached your execution limit" in str(m.get("content", ""))
            for m in messages
            if m.get("role") == "user"
        )
        if handoff:
            self.continuation = any(
                "PRIMARY_CONTEXT_MARKER" in str(m.get("content", ""))
                for m in messages
                if m.get("role") == "tool"
            )
            command = "mkdir -p .konveyor; printf '## Fixture handoff\\n- Status: completed\\nPrior work preserved.\\n' > .konveyor/handoff.md; git add .; git commit -m 'Write limit handoff'"
            return "", {
                "id": "handoff-tool",
                "type": "function",
                "function": {
                    "name": "execute",
                    "arguments": json.dumps({"command": command}),
                },
            }
        if self.case == "ask-user" and self.calls == 1:
            return "", {
                "id": "ask-tool",
                "type": "function",
                "function": {
                    "name": "ask_user",
                    "arguments": json.dumps(
                        {
                            "question": "Proceed with fixture?",
                            "options": ["Proceed", "Stop"],
                        }
                    ),
                },
            }
        if self.case == "ask-user":
            self.answer_seen = self.answer_seen or any(
                "The human answered: Proceed" in str(m.get("content", ""))
                for m in messages
                if m.get("role") == "tool"
            )
        if self.case == "cancel" and self.calls == 1:
            command = "printf 'started\\n' > started.txt; sleep 2; printf 'escaped cancellation\\n' > should-not-exist.txt"
            return "", {
                "id": "cancel-tool",
                "type": "function",
                "function": {
                    "name": "execute",
                    "arguments": json.dumps({"command": command}),
                },
            }
        if self.calls == (2 if self.case == "ask-user" else 1):
            command = (
                'set -e; test -z "${HUB_TOKEN+x}${HUB_TOKEN_ID+x}${HUB_BASE_URL+x}${APP_ID+x}"; '
                + "mkdir -p docs; printf 'fixture result\\n' > result.txt; printf '# Plan\\n\\n## Verification\\n- Build: test -f result.txt\\n' > docs/plan.md; git add result.txt docs/plan.md; git commit -m 'Implement fixture task'; printf 'PRIMARY_CONTEXT_MARKER\\n'"
            )
            if self.case == "steer-reconnect":
                command = (
                    "printf 'started\\n' > started.txt; sleep 1; "
                    + command.replace(
                        "git add result.txt docs/plan.md",
                        "git add result.txt docs/plan.md started.txt",
                    )
                )
            return "", {
                "id": "primary-tool",
                "type": "function",
                "function": {
                    "name": "execute",
                    "arguments": json.dumps({"command": command}),
                },
            }
        return "Fixture task completed.", None

    def handler(self):
        fixture = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def send_json(self, value, status=200):
                data = json.dumps(value).encode()
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

            def do_GET(self):
                path = self.path.partition("?")[0]
                fixture.hub_requests.append(
                    {
                        "path": path,
                        "authenticated": self.headers.get("Authorization")
                        == "Bearer fixture-hub-token",
                    }
                )
                if path == "/applications/42":
                    self.send_json(
                        {
                            "id": 42,
                            "name": "Local fixture",
                            "repository": {
                                "url": str(fixture.repo),
                                "branch": "main",
                                "kind": "git",
                            },
                        }
                    )
                elif path in (
                    "/applications/42/identities",
                    "/identities",
                    "/proxies",
                    "/applications/42/analysis/insights",
                ):
                    self.send_json([])
                else:
                    self.send_json({"error": self.path}, 404)

            def do_POST(self):
                if self.path == "/auth/tokens/7/revoke":
                    fixture.hub_requests.append(
                        {
                            "path": self.path,
                            "authenticated": self.headers.get("Authorization")
                            == "Bearer fixture-hub-token",
                        }
                    )
                    fixture.revoked = True
                    self.send_response(204)
                    self.end_headers()
                    return
                if self.path != "/v1/chat/completions":
                    self.send_json({"error": self.path}, 404)
                    return
                request = json.loads(
                    self.rfile.read(int(self.headers["Content-Length"]))
                )
                fixture.requests.append(request)
                text, tool = fixture.model(request)
                message = {"role": "assistant", "content": text}
                if tool:
                    message["tool_calls"] = [tool]
                usage = {
                    "prompt_tokens": 20,
                    "completion_tokens": 5,
                    "total_tokens": 25,
                }
                if not request.get("stream"):
                    self.send_json(
                        {
                            "id": "fixture",
                            "object": "chat.completion",
                            "model": "fixture-model",
                            "created": int(time.time()),
                            "choices": [
                                {
                                    "index": 0,
                                    "message": message,
                                    "finish_reason": "tool_calls" if tool else "stop",
                                }
                            ],
                            "usage": usage,
                        }
                    )
                    return
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.end_headers()
                delta = {"role": "assistant", "content": text}
                if tool:
                    delta["tool_calls"] = [{"index": 0, **tool}]
                chunks = [
                    {"choices": [{"index": 0, "delta": delta, "finish_reason": None}]},
                    {
                        "choices": [
                            {
                                "index": 0,
                                "delta": {},
                                "finish_reason": "tool_calls" if tool else "stop",
                            }
                        ]
                    },
                    {"choices": [], "usage": usage},
                ]
                for chunk in chunks:
                    chunk.update(
                        {
                            "id": "fixture",
                            "object": "chat.completion.chunk",
                            "model": "fixture-model",
                            "created": int(time.time()),
                        }
                    )
                    self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
                    self.wfile.flush()
                self.wfile.write(b"data: [DONE]\n\n")
                self.wfile.flush()

        return Handler


@contextmanager
def server(fixture: Fixture):
    http = ThreadingHTTPServer(("127.0.0.1", 0), fixture.handler())
    thread = threading.Thread(target=http.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{http.server_port}"
    finally:
        http.shutdown()
        http.server_close()
        thread.join()


async def viewer(fixture: Fixture, process: subprocess.Popen):
    from websockets.asyncio.client import connect

    deadline = time.monotonic() + 30
    while time.monotonic() < deadline and process.poll() is None:
        try:
            async with connect("ws://127.0.0.1:4000/acp?token=fixture-acp-key") as ws:
                await ws.send(
                    json.dumps(
                        {
                            "jsonrpc": "2.0",
                            "id": 1,
                            "method": "initialize",
                            "params": {
                                "protocolVersion": 1,
                                "clientCapabilities": {
                                    "_meta": {
                                        "goose": {"customNotifications": True},
                                        "elicitation": {"form": {}},
                                    }
                                },
                            },
                        }
                    )
                )
                fixture.connections += 1
                initialized = False
                loaded = False
                fixture.reconnected = fixture.connections > 1
                fixture.viewer_ready.set()
                async for raw in ws:
                    message = json.loads(raw)
                    fixture.viewer_frames.append(message)
                    if message.get("params", {}).get("sessionId"):
                        fixture.session_id = message["params"]["sessionId"]
                    if message.get("id") == 1:
                        initialized = True
                    if (
                        fixture.case == "steer-reconnect"
                        and initialized
                        and fixture.session_id
                        and not loaded
                    ):
                        loaded = True
                        await ws.send(
                            json.dumps(
                                {
                                    "jsonrpc": "2.0",
                                    "id": 3,
                                    "method": "session/load",
                                    "params": {
                                        "sessionId": fixture.session_id,
                                        "cwd": str(fixture.directory / "workspace"),
                                        "mcpServers": [],
                                    },
                                }
                            )
                        )
                    if (
                        fixture.reconnected
                        and message.get("id") == 3
                        and "error" not in message
                    ):
                        fixture.replay_received = True
                    if fixture.case == "steer-reconnect" and message.get("id") == 2:
                        assert "error" not in message, message
                        fixture.steer_accepted = True
                        await ws.close()
                        break
                    update = message.get("params", {}).get("update", {})
                    fixture.run_id = (
                        update.get("_meta", {}).get("goose", {}).get("runId")
                        or fixture.run_id
                    )
                    if (
                        fixture.case == "steer-reconnect"
                        and update.get("sessionUpdate") == "tool_call"
                        and update.get("title") == "execute"
                        and not fixture.steer_sent
                    ):
                        assert fixture.run_id, (
                            "run identity missing from session replay"
                        )
                        fixture.steer_sent = True
                        await ws.send(
                            json.dumps(
                                {
                                    "jsonrpc": "2.0",
                                    "id": 2,
                                    "method": "_goose/unstable/session/steer",
                                    "params": {
                                        "sessionId": fixture.session_id,
                                        "expectedRunId": fixture.run_id,
                                        "prompt": [
                                            {
                                                "type": "text",
                                                "text": "STEERING_CONTEXT_MARKER",
                                            }
                                        ],
                                    },
                                }
                            )
                        )
                    if (
                        fixture.case == "cancel"
                        and update.get("sessionUpdate") == "tool_call"
                        and update.get("title") == "execute"
                        and not fixture.cancel_sent
                    ):
                        for _ in range(200):
                            if (fixture.directory / "workspace/started.txt").exists():
                                break
                            await asyncio.sleep(0.01)
                        else:
                            raise RuntimeError("tool did not start before cancellation")
                        fixture.cancel_sent = True
                        await ws.send(
                            json.dumps(
                                {
                                    "jsonrpc": "2.0",
                                    "method": "session/cancel",
                                    "params": {
                                        "sessionId": message["params"]["sessionId"]
                                    },
                                }
                            )
                        )
                    if message.get("method") == "elicitation/create":
                        await ws.send(
                            json.dumps(
                                {
                                    "jsonrpc": "2.0",
                                    "id": message["id"],
                                    "result": {
                                        "action": "accept",
                                        "content": {"answer": "Proceed"},
                                    },
                                }
                            )
                        )
                    if message.get("method") == "session/request_permission":
                        options = message["params"].get("options", [])
                        choice = next(
                            option["optionId"]
                            for option in options
                            if option.get("kind") == "allow_once"
                        )
                        await ws.send(
                            json.dumps(
                                {
                                    "jsonrpc": "2.0",
                                    "id": message["id"],
                                    "result": {
                                        "outcome": {
                                            "outcome": "selected",
                                            "optionId": choice,
                                        }
                                    },
                                }
                            )
                        )
        except (OSError, TimeoutError):
            await asyncio.sleep(0.05)
        except Exception:
            if process.poll() is None:
                raise
            return


def run_case(case: str, args, binary: Path) -> dict:
    directory = args.evidence_dir / case
    directory.mkdir(parents=True)
    fixture = Fixture(directory, case)
    params = directory / "params.json"
    params.write_text(
        json.dumps(
            {
                "execution": {
                    "mode": "approve" if case == "approve" else "auto",
                    "maxTurns": 2 if case == "max-turns" else 200,
                    "askUser": case == "ask-user",
                }
            }
        )
    )
    home = directory / "home"
    home.mkdir()
    with server(fixture) as url:
        # An allowlist prevents accidental use of any developer credentials.
        env = {
            "PATH": str(args.runtime_dir / ".venv/bin")
            + ":/usr/bin:/bin:/usr/sbin:/sbin",
            "HOME": str(home),
            "LANG": "en_US.UTF-8",
            "TMPDIR": str(directory),
            "PYTHONPATH": str(args.runtime_dir / "src"),
            "PYTHONUNBUFFERED": "1",
            "HARNESS_RUNTIME": "lightspeed",
            "HARNESS_WORK_DIR": str(directory / "workspace"),
            "HARNESS_PARAMS_FILE": str(params),
            "HARNESS_TERMINATION_LOG_PATH": str(directory / "termination.json"),
            "HARNESS_SKILLS_DIR": str(directory / "empty-skills"),
            "HARNESS_HITL_TIMEOUT_SECONDS": "20",
            "HARNESS_VERIFY_ENFORCE": "on",
            "HUB_BASE_URL": url,
            "APP_ID": "42",
            "HUB_TOKEN": "fixture-hub-token",
            "HUB_TOKEN_ID": "7",
            "KONVEYOR_ACP_SECRET_KEY": "fixture-acp-key",
            "TARGET_BRANCH": "fixture-results",
            "KONVEYOR_LLM_PROVIDER": "openai",
            "KONVEYOR_LLM_MODEL": "fixture-model",
            "KONVEYOR_LLM_ENDPOINT": url + "/v1",
            "KONVEYOR_LLM_API_KEY": "fixture-local-key",
            "KONVEYOR_INSTRUCTIONS": "Complete the local fixture task and commit it.",
            "LIGHTSPEED_AGENT_TIMEOUT_SECONDS": "60",
            "LIGHTSPEED_TOOL_OUTPUT_INSPECTION_ENABLED": "true",
        }
        with (directory / "harness.log").open("w") as log:
            process = subprocess.Popen(
                [str(binary), "run"], env=env, stdout=log, stderr=subprocess.STDOUT
            )

            async def wait():
                observer = asyncio.create_task(viewer(fixture, process))
                try:
                    code = await asyncio.wait_for(asyncio.to_thread(process.wait), 90)
                    return code
                finally:
                    observer.cancel()
                    try:
                        await observer
                    except asyncio.CancelledError:
                        pass

            try:
                code = asyncio.run(wait())
            except BaseException:
                process.terminate()
                process.wait(timeout=10)
                raise
    (directory / "model-requests.json").write_text(
        json.dumps(fixture.requests, indent=2)
    )
    (directory / "viewer-frames.json").write_text(
        json.dumps(fixture.viewer_frames, indent=2)
    )
    term = json.loads((directory / "termination.json").read_text())
    expected = 2 if case == "max-turns" else 1 if case == "cancel" else 0
    assert code == expected, (
        f"{case}: exit {code}, expected {expected}; see {directory / 'harness.log'}; termination={term}"
    )
    assert fixture.revoked, f"{case}: Hub token not revoked"
    assert fixture.hub_requests and all(
        r["authenticated"] for r in fixture.hub_requests
    ), "Hub calls were unauthenticated"
    assert "fixture-hub-token" not in json.dumps(fixture.requests), (
        "Hub credential reached model context"
    )
    assert (
        "fixture-hub-token" not in (directory / "workspace/.git/config").read_text()
    ), "Hub credential reached Git config"
    if case == "cancel":
        assert fixture.cancel_sent and (directory / "workspace/started.txt").exists(), (
            "viewer did not cancel an active tool"
        )
        time.sleep(2.25)
        assert not (directory / "workspace/should-not-exist.txt").exists(), (
            "tool escaped cancellation"
        )
        assert term["stopReason"] == "cancelled", term
    else:
        assert (
            git("--git-dir", str(fixture.repo), "show", "fixture-results:result.txt")
            == "fixture result"
        )
    usage = term["usage"]
    assert usage["inputTokens"] == fixture.calls * 20, (usage, fixture.calls)
    assert usage["outputTokens"] == fixture.calls * 5, (usage, fixture.calls)
    assert "cost" not in usage, "unavailable monetary usage reported as zero"
    updates = [m.get("params", {}).get("update", {}) for m in fixture.viewer_frames]
    assert any(
        u.get("sessionUpdate") == "usage_update" and u.get("inputTokens", 0) > 0
        for u in updates
    ), "no live token usage reached viewer"
    if case != "cancel":
        assert fixture.classifier_calls > 0, "inspection path not exercised"
    if case == "max-turns":
        assert usage["turnsUsed"] <= 2, "handoff exceeded the total turn budget"
        assert term["limitReached"] == "maxTurns", term
        assert fixture.continuation, "handoff lost prior tool result/session context"
        assert git(
            "--git-dir",
            str(fixture.repo),
            "show",
            "fixture-results:.konveyor/handoff.md",
        )
    elif case != "cancel":
        assert term["verification"]["status"] == "passed", term
    if case == "steer-reconnect":
        assert fixture.steer_accepted and fixture.steer_seen, (
            "steering did not enter active model context"
        )
        assert fixture.reconnected and fixture.replay_received, (
            "viewer did not reconnect and load the live session"
        )
    if case == "ask-user":
        assert fixture.answer_seen, "human answer did not reenter model context"
        assert any(
            m.get("method") == "elicitation/create" for m in fixture.viewer_frames
        ), "question did not reach viewer"
    if case == "approve":
        assert any(
            m.get("method") == "session/request_permission"
            for m in fixture.viewer_frames
        ), "approval did not reach viewer"
    return {
        "case": case,
        "exitCode": code,
        "modelCalls": fixture.calls,
        "classifierCalls": fixture.classifier_calls,
        "usage": usage,
        "verification": term.get("verification", {}).get("status"),
        "tokenRevoked": fixture.revoked,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runtime-dir", type=Path, required=True)
    parser.add_argument("--harness", type=Path)
    parser.add_argument(
        "--evidence-dir",
        "--evidence",
        type=Path,
        default=ROOT / ".scratch/lightspeed-verification",
    )
    parser.add_argument(
        "--cases",
        nargs="+",
        choices=[
            "auto",
            "approve",
            "ask-user",
            "cancel",
            "steer-reconnect",
            "max-turns",
        ],
        default=[
            "auto",
            "approve",
            "ask-user",
            "cancel",
            "steer-reconnect",
            "max-turns",
        ],
    )
    args = parser.parse_args()
    args.runtime_dir = args.runtime_dir.resolve()
    args.evidence_dir = args.evidence_dir.resolve()
    if args.evidence_dir.exists():
        parser.error(
            f"evidence directory already exists: {args.evidence_dir}; choose a fresh path"
        )
    args.evidence_dir.mkdir(parents=True)
    binary = (
        args.harness.resolve()
        if args.harness
        else args.evidence_dir / "migration-harness"
    )
    if not args.harness:
        subprocess.run(
            ["go", "build", "-o", str(binary), "./cmd/migration-harness"],
            cwd=ROOT / "harness",
            check=True,
        )
    results = []
    for case in args.cases:
        result = run_case(case, args, binary)
        results.append(result)
        print(json.dumps(result), flush=True)
    (args.evidence_dir / "summary.json").write_text(
        json.dumps(results, indent=2) + "\n"
    )


if __name__ == "__main__":
    main()
