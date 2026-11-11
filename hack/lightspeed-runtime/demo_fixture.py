#!/usr/bin/env python3
"""Deterministic local model/Git rehearsal for the real Hub and browser UI.

This fixture scripts model decisions, never tool execution or runtime provenance.
Every take has a fresh Git repository, model history, and explicit control gates.
"""

from __future__ import annotations

import argparse
import json
import re
import signal
import socket
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

from verify import Fixture, git

QUESTION = "Which environment should this rehearsal target?"
OPTIONS = ["staging", "production"]
STEERING_TEXT = "Use the canary rollout label."
PROVENANCE_MARKER = "DEMO_PROVENANCE_JSON:"
RESULT_MARKER = "DEMO_RESULT_JSON:"


def _tool(name: str, arguments: dict, call_id: str) -> dict:
    return {
        "id": call_id,
        "type": "function",
        "function": {"name": name, "arguments": json.dumps(arguments)},
    }


def _tool_json(messages: list[dict], marker: str) -> dict:
    for message in reversed(messages):
        if message.get("role") != "tool":
            continue
        content = str(message.get("content", ""))
        match = re.search(re.escape(marker) + r"(\{[^\n]+\})", content)
        if match:
            return json.loads(match.group(1))
    raise RuntimeError("Expected actual tool evidence is missing")


def provenance_command(control_url: str) -> str:
    """Read the actual imported package/process, then hold the active tool for steering."""
    code = """import hashlib, importlib.metadata, inspect, json, os, pathlib, platform, time, urllib.request
import lightspeed_agentic
from lightspeed_agentic.session import RuntimeSession
processes = []
for path in pathlib.Path('/proc').glob('[0-9]*/cmdline'):
    try:
        argv = path.read_bytes().decode().strip('\\0').split('\\0')
    except (OSError, UnicodeError):
        continue
    if any(argv[i:i+2] == ['-m', 'lightspeed_agentic.acp'] for i in range(len(argv)-1)):
        processes.append({'pid': int(path.parent.name), 'python': argv[0], 'module': 'lightspeed_agentic.acp'})
if not processes:
    raise RuntimeError('No actual Lightspeed ACP process found')
if any(key in os.environ for key in ['HUB_TOKEN', 'HUB_TOKEN_ID', 'HUB_BASE_URL', 'APP_ID']):
    raise RuntimeError('Harness credentials reached the tool environment')
source = pathlib.Path(inspect.getsourcefile(RuntimeSession))
provenance = {'runtime': 'lightspeed-agentic', 'version': importlib.metadata.version('lightspeed-agentic'), 'module_path': lightspeed_agentic.__file__, 'session_module_path': str(source), 'session_source_sha256': hashlib.sha256(source.read_bytes()).hexdigest(), 'deepagents_version': importlib.metadata.version('deepagents'), 'python_version': platform.python_version(), 'acp_processes': processes, 'harness_credentials_in_environment': False}
pathlib.Path('runtime-provenance.json').write_text(json.dumps(provenance, indent=2) + '\\n')
print('DEMO_PROVENANCE_JSON:' + json.dumps(provenance), flush=True)
control = CONTROL_URL
request = urllib.request.Request(control + '/provenance-ready', data=json.dumps(provenance).encode(), headers={'Content-Type':'application/json'}, method='POST')
with urllib.request.urlopen(request, timeout=5) as response:
    response.read()
print('Waiting for the user to send guidance before the next model call.', flush=True)
deadline = time.monotonic() + 100
while time.monotonic() < deadline:
    with urllib.request.urlopen(control + '/tool-gate', timeout=5) as response:
        if json.load(response)['open']:
            break
    time.sleep(0.25)
else:
    raise TimeoutError('The steering rehearsal gate was not released')
print('Steering window complete. Returning to the model.', flush=True)
"""
    code = code.replace("CONTROL_URL", repr(control_url))
    return "python3 - <<'LIGHTSPEED_PROVENANCE'\n" + code + "LIGHTSPEED_PROVENANCE"


def commit_command(environment: str) -> str:
    """Create and independently verify real output using the selected target and guidance."""
    verify = """import json, pathlib
result = json.loads(pathlib.Path('demo-result.json').read_text())
assert result['environment'] == EXPECTED_ENVIRONMENT
assert result['rollout'] == 'canary'
runtime = result['runtime']
assert runtime['runtime'] == 'lightspeed-agentic'
assert runtime['module_path'].endswith('/lightspeed_agentic/__init__.py')
assert runtime['session_module_path'].endswith('/lightspeed_agentic/session.py')
assert len(runtime['session_source_sha256']) == 64
assert runtime['acp_processes'] and all(p['module'] == 'lightspeed_agentic.acp' for p in runtime['acp_processes'])
assert runtime['harness_credentials_in_environment'] is False
print('Verified: user target, canary guidance, and actual Lightspeed runtime provenance.')
""".replace("EXPECTED_ENVIRONMENT", repr(environment))
    code = """import json, pathlib
runtime = json.loads(pathlib.Path('runtime-provenance.json').read_text())
result = {'environment': ENVIRONMENT, 'rollout': 'canary', 'runtime': runtime}
pathlib.Path('demo-result.json').write_text(json.dumps(result, indent=2) + '\\n')
pathlib.Path('result.txt').write_text('Target: ' + result['environment'] + '\\nRollout: canary\\nRuntime: lightspeed-agentic\\n')
pathlib.Path('verify_demo.py').write_text(VERIFICATION_SOURCE)
pathlib.Path('docs').mkdir(exist_ok=True)
pathlib.Path('docs/plan.md').write_text('# Lightspeed scripted rehearsal\\n\\n## Verification\\n- Build: python3 verify_demo.py\\n')
""".replace("ENVIRONMENT", repr(environment)).replace(
        "VERIFICATION_SOURCE", repr(verify)
    )
    result = """import json, pathlib, subprocess
result = json.loads(pathlib.Path('demo-result.json').read_text())
result['commit'] = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
print('DEMO_RESULT_JSON:' + json.dumps(result))
"""
    return (
        "set -e\npython3 - <<'LIGHTSPEED_RESULT'\n"
        + code
        + "LIGHTSPEED_RESULT\npython3 verify_demo.py\n"
        + "git add runtime-provenance.json demo-result.json result.txt verify_demo.py docs/plan.md\n"
        + "git commit -m 'Apply the user-selected target and canary guidance'\n"
        + "python3 - <<'LIGHTSPEED_COMMIT'\n"
        + result
        + "LIGHTSPEED_COMMIT"
    )


class DemoFixture(Fixture):
    def __init__(self, directory: Path, name: str, control_url: str):
        super().__init__(directory, "scripted-demo")
        self.name = name
        self.control_url = control_url
        self.stage = "created"
        self.tool_release = threading.Event()
        self.provenance_ready = threading.Event()
        self.runtime_provenance: dict | None = None
        self.result: dict | None = None
        self.environment: str | None = None
        self.guidance: str | None = None
        self.failure: str | None = None

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
        if self.calls == 1:
            self.stage = "awaiting_viewer"
            self.model_ready.set()
            if not self.viewer_ready.wait(180):
                raise RuntimeError("No viewer attached before the rehearsal deadline")
            self.stage = "asking_user"
            return "First, I need your target environment.", _tool(
                "ask_user", {"question": QUESTION, "options": OPTIONS}, "demo-question"
            )
        if self.calls == 2:
            self.environment = next(
                (
                    choice
                    for choice in OPTIONS
                    if any(
                        f"The human answered: {choice}" in str(m.get("content", ""))
                        for m in messages
                        if m.get("role") == "tool"
                    )
                ),
                None,
            )
            if self.environment is None:
                raise RuntimeError(
                    "The trusted user answer is missing from model history"
                )
            self.stage = "collecting_provenance"
            return (
                f"You selected {self.environment}. I will inspect the actual Lightspeed runtime, then wait for your guidance.",
                _tool(
                    "execute",
                    {"command": provenance_command(self.control_url), "timeout": 120},
                    "demo-provenance",
                ),
            )
        if self.calls == 3:
            provenance = _tool_json(messages, PROVENANCE_MARKER)
            if (
                provenance.get("runtime") != "lightspeed-agentic"
                or not provenance.get("acp_processes")
                or not provenance.get("session_source_sha256")
            ):
                raise RuntimeError(
                    "The actual tool did not identify the Lightspeed runtime"
                )
            self.runtime_provenance = provenance
            self.guidance = next(
                (
                    str(m.get("content", ""))
                    for m in reversed(messages)
                    if m.get("role") == "user"
                    and STEERING_TEXT in str(m.get("content", ""))
                ),
                None,
            )
            if self.guidance is None:
                raise RuntimeError(
                    "The user's canary guidance did not reach the next model call"
                )
            self.steer_seen = True
            self.stage = "applying_guidance"
            return (
                f"I received your guidance: canary rollout for {self.environment}. I will apply it and commit the verified result.",
                _tool(
                    "execute",
                    {"command": commit_command(self.environment)},
                    "demo-commit",
                ),
            )
        if self.calls == 4:
            result = _tool_json(messages, RESULT_MARKER)
            if (
                result.get("environment") != self.environment
                or result.get("rollout") != "canary"
                or not re.fullmatch(r"[a-f0-9]{40,64}", result.get("commit", ""))
            ):
                raise RuntimeError(
                    "The committed tool result does not match the user decisions"
                )
            self.result = result
            self.stage = "completed"
            runtime = result["runtime"]
            message = (
                f"Applied your decisions: {result['environment']} target, canary rollout.\n\n"
                f"Actual runtime: lightspeed-agentic {runtime['version']}; DeepAgents {runtime['deepagents_version']}.\n"
                f"Imported module: {runtime['module_path']}\n"
                "Process: python -m lightspeed_agentic.acp\n"
                f"Session source SHA256: {runtime['session_source_sha256'][:16]}\n"
                f"Commit: {result['commit']}\n\n"
                "This rehearsal used the scripted local model. The tools, runtime inspection, verification, and Git commit ran in the actual workload."
            )
            return message, None
        raise RuntimeError(
            "The scripted rehearsal received an unexpected extra model call"
        )

    def snapshot(self) -> dict:
        branches = {}
        for branch in git(
            "--git-dir",
            str(self.repo),
            "for-each-ref",
            "--format=%(refname:short)",
            "refs/heads",
        ).splitlines():
            if branch == "main":
                continue
            try:
                branches[branch] = {
                    "commit": git("--git-dir", str(self.repo), "rev-parse", branch),
                    "result": json.loads(
                        git(
                            "--git-dir",
                            str(self.repo),
                            "show",
                            branch + ":demo-result.json",
                        )
                    ),
                }
            except (subprocess.CalledProcessError, json.JSONDecodeError):
                continue
        return {
            "name": self.name,
            "scriptedModel": True,
            "stage": self.stage,
            "modelCalls": self.calls,
            "classifierCalls": self.classifier_calls,
            "viewerGateOpen": self.viewer_ready.is_set(),
            "provenanceReady": self.provenance_ready.is_set(),
            "toolGateOpen": self.tool_release.is_set(),
            "environment": self.environment,
            "steeringSeen": self.steer_seen,
            "guidance": self.guidance,
            "runtimeProvenance": self.runtime_provenance,
            "result": self.result,
            "pushedBranches": branches,
            "failure": self.failure,
        }


def make_handler(
    data: Path, hostname: str, port: int, git_port: int
) -> type[BaseHTTPRequestHandler]:
    fixtures: dict[str, DemoFixture] = {}
    lock = threading.Lock()

    class Router(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def json_body(self) -> dict:
            size = int(self.headers.get("Content-Length", "0"))
            return json.loads(self.rfile.read(size)) if size else {}

        def send_json(self, value, status=200):
            raw = json.dumps(value).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def route(self):
            path = urlsplit(self.path).path
            if path == "/health":
                self.send_json({"healthy": True, "fixture": "lightspeed-scripted-demo"})
                return
            if self.command == "POST" and path == "/sessions":
                name = self.json_body().get("name", "")
                if not isinstance(name, str) or not re.fullmatch(
                    r"[a-z0-9][a-z0-9-]{0,50}", name
                ):
                    self.send_json(
                        {"error": "Use a lowercase rehearsal session name"}, 400
                    )
                    return
                with lock:
                    if name in fixtures or (data / name).exists():
                        self.send_json(
                            {"error": "Take already exists; use a fresh session name"},
                            409,
                        )
                        return
                    directory = data / name
                    directory.mkdir()
                    control = f"http://{hostname}:{port}/sessions/{name}"
                    fixture = DemoFixture(directory, name, control)
                    git(
                        "--git-dir",
                        str(fixture.repo),
                        "config",
                        "daemon.receivepack",
                        "true",
                    )
                    fixtures[name] = fixture
                self.send_json(
                    {
                        "name": name,
                        "repository": f"git://{hostname}:{git_port}/{name}/remote.git",
                        "control": control,
                    },
                    201,
                )
                return
            match = re.fullmatch(r"/sessions/([a-z0-9-]+)(/.*)", path)
            fixture = fixtures.get(match.group(1)) if match else None
            if fixture is None:
                self.send_json({"error": "Unknown rehearsal session"}, 404)
                return
            tail = match.group(2)
            if self.command == "GET" and tail == "/state":
                self.send_json(fixture.snapshot())
            elif self.command == "GET" and tail == "/tool-gate":
                self.send_json({"open": fixture.tool_release.is_set()})
            elif self.command == "POST" and tail == "/viewer-ready":
                fixture.viewer_ready.set()
                self.send_json({"open": True})
            elif self.command == "POST" and tail == "/release-tool":
                fixture.tool_release.set()
                self.send_json({"open": True})
            elif self.command == "POST" and tail == "/provenance-ready":
                fixture.runtime_provenance = self.json_body()
                fixture.provenance_ready.set()
                fixture.stage = "awaiting_steering"
                self.send_json({"ready": True})
            elif self.command == "GET" and tail in ("/v1/models", "/v1/v1/models"):
                self.send_json(
                    {
                        "object": "list",
                        "data": [{"id": "fixture-model", "object": "model"}],
                    }
                )
            elif self.command == "POST" and tail == "/v1/chat/completions":
                self.path = tail
                try:
                    fixture.handler().do_POST(self)
                except (RuntimeError, OSError, KeyError, ValueError) as exc:
                    fixture.failure = type(exc).__name__ + ": " + str(exc)
                    fixture.stage = "failed"
                    self.send_json(
                        {
                            "error": {
                                "message": fixture.failure,
                                "type": "scripted_rehearsal_failed",
                            }
                        },
                        500,
                    )
            else:
                self.send_json({"error": "Unknown rehearsal operation"}, 404)

        def do_GET(self):
            self.route()

        def do_POST(self):
            self.route()

    return Router


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, default=Path("/data"))
    parser.add_argument("--hostname", required=True)
    parser.add_argument("--port", type=int, default=8080)
    parser.add_argument("--git-port", type=int, default=9418)
    args = parser.parse_args()
    args.data.mkdir(parents=True, exist_ok=True)
    daemon = subprocess.Popen(
        [
            "git",
            "daemon",
            "--export-all",
            "--enable=receive-pack",
            "--reuseaddr",
            "--listen=0.0.0.0",
            f"--port={args.git_port}",
            f"--base-path={args.data}",
            str(args.data),
        ]
    )
    try:
        for _ in range(100):
            if daemon.poll() is not None:
                raise RuntimeError("The local Git fixture exited")
            try:
                with socket.create_connection(
                    ("127.0.0.1", args.git_port), timeout=0.1
                ):
                    break
            except OSError:
                time.sleep(0.05)
        else:
            raise TimeoutError("The local Git fixture did not become ready")

        def terminate(_signum, _frame):
            raise SystemExit(0)

        signal.signal(signal.SIGTERM, terminate)
        ThreadingHTTPServer(
            ("0.0.0.0", args.port),
            make_handler(args.data, args.hostname, args.port, args.git_port),
        ).serve_forever()
    finally:
        daemon.terminate()
        daemon.wait(timeout=5)


if __name__ == "__main__":
    main()
