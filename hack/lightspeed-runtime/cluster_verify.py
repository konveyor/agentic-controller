#!/usr/bin/env python3
"""Verify real AgentRuns/Sandboxes in a prepared disposable Kind cluster.

Requires the controller, Agent Sandbox, a loaded prototype runtime image, and a
Python environment with websockets. All Hub, model, and Git services are fixtures.
"""

from __future__ import annotations

import argparse
import asyncio
import base64
import json
import socket
import subprocess
import time
from contextlib import contextmanager
from pathlib import Path
from urllib.request import urlopen

HERE = Path(__file__).resolve().parent


class Cluster:
    def __init__(self, args):
        self.args = args

    def kubectl(self, *parts, data=None, check=True):
        result = subprocess.run(
            [
                "kubectl",
                "--kubeconfig",
                str(self.args.kubeconfig),
                "-n",
                self.args.namespace,
                *parts,
            ],
            input=data,
            text=True,
            capture_output=True,
            check=False,
        )
        if check and result.returncode:
            raise RuntimeError(f"kubectl {' '.join(parts)}: {result.stderr.strip()}")
        return result.stdout

    def get(self, kind, name=None):
        return json.loads(
            self.kubectl("get", kind, *([name] if name else []), "-o", "json")
        )

    def apply(self, objects):
        self.kubectl(
            "apply",
            "-f",
            "-",
            data=json.dumps({"apiVersion": "v1", "kind": "List", "items": objects}),
        )

    async def wait(self, kind, name, predicate, timeout=240):
        deadline = time.monotonic() + timeout
        last = None
        while time.monotonic() < deadline:
            last = await asyncio.to_thread(self.get, kind, name)
            if predicate(last):
                return last
            await asyncio.sleep(0.5)
        raise TimeoutError(f"{kind}/{name}: {last.get('status')}")

    @contextmanager
    def forward(self, resource, remote, local, log_name):
        with (self.args.evidence / log_name).open("w") as log:
            process = subprocess.Popen(
                [
                    "kubectl",
                    "--kubeconfig",
                    str(self.args.kubeconfig),
                    "-n",
                    self.args.namespace,
                    "port-forward",
                    resource,
                    f"{local}:{remote}",
                    "--address",
                    "127.0.0.1",
                ],
                stdout=log,
                stderr=subprocess.STDOUT,
            )
            try:
                deadline = time.monotonic() + 20
                while time.monotonic() < deadline:
                    if process.poll() is not None:
                        raise RuntimeError(f"port-forward stopped; see {log_name}")
                    try:
                        with socket.create_connection(
                            ("127.0.0.1", local), timeout=0.1
                        ):
                            break
                    except OSError:
                        time.sleep(0.1)
                else:
                    raise TimeoutError(f"port-forward {resource} did not listen")
                yield
            finally:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()


def ready(resource):
    return any(
        c.get("type") == "Ready" and c.get("status") == "True"
        for c in resource.get("status", {}).get("conditions", [])
    )


def base(kind, name, spec, namespace, version="konveyor.io/v1alpha1"):
    return {
        "apiVersion": version,
        "kind": kind,
        "metadata": {"name": name, "namespace": namespace},
        "spec": spec,
    }


def fixture_state(case):
    with urlopen(f"http://127.0.0.1:18080/{case}/state", timeout=10) as response:
        return json.load(response)


async def observe(case, secret, done, frames):
    from websockets.asyncio.client import connect

    deadline = time.monotonic() + 120
    while time.monotonic() < deadline and not done.is_set():
        try:
            async with connect(f"ws://127.0.0.1:18400/acp?token={secret}") as ws:
                await ws.send(
                    json.dumps(
                        {
                            "jsonrpc": "2.0",
                            "id": 1,
                            "method": "initialize",
                            "params": {
                                "protocolVersion": 1,
                                "clientCapabilities": {
                                    "_meta": {"goose": {"customNotifications": True}}
                                },
                            },
                        }
                    )
                )
                await asyncio.to_thread(
                    lambda: urlopen(
                        f"http://127.0.0.1:18080/{case}/viewer-ready", timeout=5
                    ).close()
                )
                async for raw in ws:
                    message = json.loads(raw)
                    frames.append(message)
                    if message.get("method") == "session/request_permission":
                        choice = next(
                            o["optionId"]
                            for o in message["params"]["options"]
                            if o.get("kind") == "allow_once"
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
            return
        except (OSError, TimeoutError):
            await asyncio.sleep(0.1)
        except Exception:
            if done.is_set():
                return
            raise
    if not done.is_set():
        raise TimeoutError(f"viewer did not attach to {case}")


async def execute_case(cluster, case):
    args = cluster.args
    prefix = "lightspeed-" + case
    service = f"lightspeed-fixture.{args.namespace}.svc"
    env = {
        "HUB_BASE_URL": f"http://{service}:8080/{case}",
        "HUB_TOKEN": "fixture-hub-token",
        "HUB_TOKEN_ID": "7",
        "APP_ID": "42",
        "TARGET_BRANCH": "fixture-results",
        "HARNESS_RUNTIME": "lightspeed",
        "HARNESS_VERIFY_ENFORCE": "on",
        "LIGHTSPEED_AGENT_TIMEOUT_SECONDS": "120",
        "LIGHTSPEED_TOOL_OUTPUT_INSPECTION_ENABLED": "true",
    }
    cluster.apply(
        [
            {
                "apiVersion": "v1",
                "kind": "Secret",
                "metadata": {"name": prefix + "-provider", "namespace": args.namespace},
                "stringData": {"api-key": "fixture-local-key"},
            },
            base(
                "Gateway",
                prefix,
                {
                    "provider": "openai",
                    "endpoint": f"http://{service}:8080/{case}/v1",
                    "credentialRef": {
                        "secretName": prefix + "-provider",
                        "key": "api-key",
                    },
                    "model": {"name": "fixture-model", "contextWindow": 128000},
                },
                args.namespace,
            ),
            base(
                "Agent",
                prefix,
                {
                    "image": args.image,
                    "prompt": "Complete the local fixture task and commit it.",
                    "gateways": [{"ref": prefix}],
                    "execution": {"maxTurns": 2 if case == "max-turns" else 200},
                },
                args.namespace,
            ),
        ]
    )
    await cluster.wait("gateway", prefix, ready)
    await cluster.wait("agent", prefix, ready)
    cluster.apply(
        [
            base(
                "AgentRun",
                prefix,
                {
                    "agentRef": prefix,
                    "gateway": prefix,
                    "instructions": "Execute the scripted fixture task.",
                    "execution": {"mode": "approve" if case == "approve" else "auto"},
                    "env": [{"name": k, "value": v} for k, v in env.items()],
                },
                args.namespace,
            )
        ]
    )
    run = await cluster.wait(
        "agentrun",
        prefix,
        lambda r: bool(
            r.get("status", {}).get("sandboxName")
            and r.get("status", {}).get("secretKeyRef")
        ),
    )
    sandbox = run["status"]["sandboxName"]
    pod = None
    deadline = time.monotonic() + 240
    while time.monotonic() < deadline:
        pods = cluster.get("pods")["items"]
        matches = [
            p
            for p in pods
            if p["metadata"]["name"] == sandbox
            or any(
                o.get("kind") == "Sandbox" and o.get("name") == sandbox
                for o in p["metadata"].get("ownerReferences", [])
            )
        ]
        if matches and matches[0].get("status", {}).get("phase") == "Running":
            pod = matches[0]
            break
        await asyncio.sleep(0.5)
    if pod is None:
        raise TimeoutError(f"Sandbox {sandbox} has no running pod")
    # Read only this fixture run's newly generated ACP key; do not persist it.
    key = cluster.get("secret", run["status"]["secretKeyRef"]["name"])["data"][
        "secret-key"
    ]
    secret = base64.b64decode(key).decode()
    done = asyncio.Event()
    frames = []
    with cluster.forward(
        "pod/" + pod["metadata"]["name"], 4000, 18400, prefix + "-port-forward.log"
    ):
        observer = asyncio.create_task(observe(case, secret, done, frames))
        try:
            final = await cluster.wait(
                "agentrun",
                prefix,
                lambda r: r.get("status", {}).get("phase") in ("Succeeded", "Failed"),
                timeout=180,
            )
        finally:
            done.set()
            observer.cancel()
            try:
                await observer
            except asyncio.CancelledError:
                pass
    state = fixture_state(case)
    term = final["status"]["terminationData"]
    if isinstance(term, str):
        term = json.loads(term)
    for suffix, value in (
        ("agentrun", final),
        ("sandbox", cluster.get("sandbox", sandbox)),
        ("fixture", state),
        ("viewer", frames),
    ):
        (args.evidence / f"{prefix}-{suffix}.json").write_text(
            json.dumps(value, indent=2)
        )
    (args.evidence / (prefix + "-harness.log")).write_text(
        cluster.kubectl("logs", pod["metadata"]["name"], "-c", "agent", check=False)
    )
    assert state["revoked"] and all(r["authenticated"] for r in state["hubRequests"]), (
        "Hub authentication/revocation missing"
    )
    assert state["files"]["result.txt"] == "fixture result", (
        "agent commit was not pushed"
    )
    assert (
        term["usage"]["inputTokens"] == state["modelCalls"] * 20
        and term["usage"]["outputTokens"] == state["modelCalls"] * 5
    ), (term, state["modelCalls"])
    assert "cost" not in term["usage"], "absent cost became measured zero"
    assert any(m.get("method") == "_lightspeed/session/update" for m in frames), (
        "live usage absent from viewer"
    )
    assert state["classifierCalls"] > 0, "inspection not exercised"
    if case == "max-turns":
        assert term["exitCode"] == 2 and term["limitReached"] == "maxTurns", term
        assert state["continuation"] and state["files"][".konveyor/handoff.md"], (
            "same-session handoff missing"
        )
        assert term["usage"]["turnsUsed"] <= 2, "handoff exceeded total turn allowance"
    else:
        assert (
            final["status"]["phase"] == "Succeeded"
            and term["verification"]["status"] == "passed"
        ), term
    if case == "approve":
        assert any(m.get("method") == "session/request_permission" for m in frames), (
            "approval did not reach viewer"
        )
    return {
        "case": case,
        "agentRun": prefix,
        "sandbox": sandbox,
        "phase": final["status"]["phase"],
        "usage": term["usage"],
        "exitCode": term["exitCode"],
        "tokenRevoked": state["revoked"],
    }


async def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kubeconfig", type=Path, required=True)
    parser.add_argument("--image", required=True)
    parser.add_argument("--namespace", default="lightspeed-validation")
    parser.add_argument("--evidence", type=Path, required=True)
    args = parser.parse_args()
    args.kubeconfig = args.kubeconfig.resolve()
    args.evidence = args.evidence.resolve()
    args.evidence.mkdir(parents=True, exist_ok=False)
    cluster = Cluster(args)
    cluster.apply(
        [
            {
                "apiVersion": "v1",
                "kind": "Namespace",
                "metadata": {"name": args.namespace},
            }
        ]
    )
    hostname = f"lightspeed-fixture.{args.namespace}.svc"
    pod = base(
        "Pod",
        "lightspeed-fixture",
        {
            "restartPolicy": "Never",
            "securityContext": {"fsGroup": 1001},
            "containers": [
                {
                    "name": "fixture",
                    "image": args.image,
                    "imagePullPolicy": "IfNotPresent",
                    "command": [
                        "python3",
                        "/fixture/fixture_server.py",
                        "--hostname",
                        hostname,
                    ],
                    "ports": [{"containerPort": 8080}, {"containerPort": 9418}],
                    "volumeMounts": [
                        {"name": "scripts", "mountPath": "/fixture"},
                        {"name": "data", "mountPath": "/data"},
                    ],
                    "readinessProbe": {
                        "httpGet": {"path": "/health", "port": 8080},
                        "initialDelaySeconds": 1,
                        "periodSeconds": 1,
                    },
                }
            ],
            "volumes": [
                {"name": "scripts", "configMap": {"name": "lightspeed-fixture"}},
                {"name": "data", "emptyDir": {}},
            ],
        },
        args.namespace,
        "v1",
    )
    pod["metadata"]["labels"] = {"fixture": "lightspeed"}
    cluster.apply(
        [
            {
                "apiVersion": "v1",
                "kind": "ConfigMap",
                "metadata": {"name": "lightspeed-fixture", "namespace": args.namespace},
                "data": {
                    p.name: p.read_text()
                    for p in (HERE / "verify.py", HERE / "fixture_server.py")
                },
            },
            pod,
            base(
                "Service",
                "lightspeed-fixture",
                {
                    "selector": {"fixture": "lightspeed"},
                    "ports": [
                        {"name": "http", "port": 8080},
                        {"name": "git", "port": 9418},
                    ],
                },
                args.namespace,
                "v1",
            ),
        ]
    )
    await cluster.wait("pod", "lightspeed-fixture", ready)
    with cluster.forward(
        "service/lightspeed-fixture", 8080, 18080, "fixture-port-forward.log"
    ):
        results = []
        for case in ("auto", "approve", "max-turns"):
            result = await execute_case(cluster, case)
            results.append(result)
            print(json.dumps(result), flush=True)
        (args.evidence / "summary.json").write_text(
            json.dumps(results, indent=2) + "\n"
        )


if __name__ == "__main__":
    asyncio.run(main())
