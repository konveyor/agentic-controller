#!/usr/bin/env python3
"""In-cluster local model/Hub/Git fixture; contains no real credentials."""

from __future__ import annotations

import argparse
import json
import signal
import socket
import subprocess
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

from verify import Fixture, git


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, default=Path("/data"))
    parser.add_argument("--hostname", required=True)
    parser.add_argument("--port", type=int, default=8080)
    parser.add_argument("--git-port", type=int, default=9418)
    args = parser.parse_args()
    fixtures = {}
    for case in (
        "auto",
        "approve",
        "ask-user",
        "cancel",
        "steer-reconnect",
        "max-turns",
    ):
        directory = args.data / case
        directory.mkdir(parents=True)
        fixtures[case] = Fixture(directory, case)
        fixtures[case].viewer_timeout = 120
        git(
            "--git-dir",
            str(fixtures[case].repo),
            "config",
            "daemon.receivepack",
            "true",
        )
    daemon = subprocess.Popen(
        [
            "git",
            "daemon",
            "--export-all",
            "--enable=receive-pack",
            "--reuseaddr",
            "--listen=0.0.0.0",
            "--port=" + str(args.git_port),
            "--base-path=" + str(args.data),
            str(args.data),
        ]
    )

    # Do not advertise readiness until the Git listener can accept clones.
    for _ in range(100):
        if daemon.poll() is not None:
            raise RuntimeError("fixture Git daemon exited before readiness")
        try:
            with socket.create_connection(("127.0.0.1", args.git_port), timeout=0.1):
                break
        except OSError:
            time.sleep(0.01)
    else:
        daemon.terminate()
        daemon.wait(timeout=5)
        raise RuntimeError("fixture Git daemon did not become ready")

    class Router(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def send_json(self, value, status=200):
            data = json.dumps(value).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def route(self):
            parts = urlsplit(self.path).path.strip("/").split("/", 1)
            fixture = fixtures.get(parts[0])
            if fixture is None or len(parts) != 2:
                self.send_json({"error": "unknown fixture"}, 404)
                return
            path = "/" + parts[1]
            if path in ("/v1/models", "/v1/v1/models"):
                self.send_json(
                    {
                        "object": "list",
                        "data": [{"id": "fixture-model", "object": "model"}],
                    }
                )
            elif path == "/viewer-ready":
                fixture.viewer_ready.set()
                self.send_json({"ready": True})
            elif path == "/state":
                files = {}
                for name in ("result.txt", ".konveyor/handoff.md"):
                    try:
                        files[name] = git(
                            "--git-dir",
                            str(fixture.repo),
                            "show",
                            "fixture-results:" + name,
                        )
                    except subprocess.CalledProcessError:
                        files[name] = None
                self.send_json(
                    {
                        "case": fixture.case,
                        "revoked": fixture.revoked,
                        "modelCalls": fixture.calls,
                        "classifierCalls": fixture.classifier_calls,
                        "continuation": fixture.continuation,
                        "hubRequests": fixture.hub_requests,
                        "modelRequests": fixture.requests,
                        "files": files,
                    }
                )
            elif path == "/applications/42":
                fixture.hub_requests.append(
                    {
                        "path": path,
                        "authenticated": self.headers.get("Authorization")
                        == "Bearer fixture-hub-token",
                    }
                )
                self.send_json(
                    {
                        "id": 42,
                        "name": "Cluster fixture",
                        "repository": {
                            "url": f"git://{args.hostname}:{args.git_port}/{fixture.case}/remote.git",
                            "branch": "main",
                            "kind": "git",
                        },
                    }
                )
            else:
                self.path = path + (
                    "?" + urlsplit(self.path).query if urlsplit(self.path).query else ""
                )
                handler = fixture.handler()
                if self.command == "GET":
                    handler.do_GET(self)
                elif self.command == "POST":
                    handler.do_POST(self)
                else:
                    self.send_json({"error": "unsupported method"}, 405)

        def do_GET(self):
            if self.path == "/health":
                self.send_json({"healthy": daemon.poll() is None})
            else:
                self.route()

        def do_POST(self):
            self.route()

    def terminate(_signum, _frame):
        raise SystemExit(0)

    signal.signal(signal.SIGTERM, terminate)
    try:
        ThreadingHTTPServer(("0.0.0.0", args.port), Router).serve_forever()
    finally:
        daemon.terminate()
        daemon.wait(timeout=5)


if __name__ == "__main__":
    main()
