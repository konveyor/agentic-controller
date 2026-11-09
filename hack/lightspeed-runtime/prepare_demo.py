#!/usr/bin/env python3
"""Prepare one fresh scripted Lightspeed UI rehearsal without creating its run.

Use the resulting setup.json in the browser recorder. This uses the actual Hub,
controller, runtime image, tools, and Git; only the model decisions are scripted.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import socket
import subprocess
import time
from datetime import datetime, timezone
from pathlib import Path
from urllib.request import Request, urlopen

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kubeconfig", type=Path, required=True)
    parser.add_argument(
        "--image", required=True, help="Committed, loaded runtime image for the Agent"
    )
    parser.add_argument(
        "--fixture-image", default="localhost/agent-lightspeed:prototype"
    )
    parser.add_argument("--namespace", default="lightspeed-konveyor")
    parser.add_argument("--fixture-name", default="lightspeed-demo-fixture")
    parser.add_argument(
        "--session",
        default="rehearsal-" + datetime.now(timezone.utc).strftime("%Y%m%d-%H%M%S"),
    )
    parser.add_argument("--hub", default="http://127.0.0.1:18081/hub")
    parser.add_argument("--control-port", type=int, default=18085)
    parser.add_argument("--evidence", type=Path)
    args = parser.parse_args()
    args.kubeconfig = args.kubeconfig.resolve()
    evidence = (
        args.evidence or ROOT / ".scratch/lightspeed-demo" / args.session
    ).resolve()
    evidence.mkdir(parents=True, exist_ok=False)
    ns, fixture_name = args.namespace, args.fixture_name
    hostname = f"{fixture_name}.{ns}.svc"
    control = f"http://127.0.0.1:{args.control_port}"

    def kubectl(*parts, data=None, check=True):
        p = subprocess.run(
            ["kubectl", "--kubeconfig", str(args.kubeconfig), "-n", ns, *parts],
            input=data,
            text=True,
            capture_output=True,
            check=False,
        )
        if check and p.returncode:
            raise RuntimeError(p.stderr.strip())
        return p.stdout

    def get(kind, name):
        return json.loads(kubectl("get", kind, name, "-o", "json"))

    def apply(items):
        kubectl(
            "apply",
            "-f",
            "-",
            data=json.dumps({"apiVersion": "v1", "kind": "List", "items": items}),
        )

    def http(url, body=None):
        request = Request(
            url,
            data=json.dumps(body).encode() if body is not None else None,
            headers={"Content-Type": "application/json"},
        )
        with urlopen(request, timeout=15) as r:
            return json.load(r)

    def resource(kind, name, spec):
        return {
            "apiVersion": "konveyor.io/v1alpha1",
            "kind": kind,
            "metadata": {"name": name, "namespace": ns},
            "spec": spec,
        }

    def ready(value):
        return any(
            c.get("type") == "Ready" and c.get("status") == "True"
            for c in value.get("status", {}).get("conditions", [])
        )

    def wait(kind, name, predicate, timeout=240):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            value = get(kind, name)
            if predicate(value):
                return value
            time.sleep(0.5)
        raise TimeoutError(f"{kind}/{name} did not become ready")

    scripts = {
        p.name: p.read_text() for p in [HERE / "verify.py", HERE / "demo_fixture.py"]
    }
    source_hash = hashlib.sha256(
        json.dumps(scripts, sort_keys=True).encode()
    ).hexdigest()
    existing = kubectl("get", "pod", fixture_name, "-o", "json", "--ignore-not-found")
    if existing:
        pod = json.loads(existing)
        if (
            pod["metadata"].get("annotations", {}).get("lightspeed-demo/source-sha256")
            != source_hash
        ):
            raise RuntimeError(
                "Fixture code changed; select a fresh --fixture-name and --control-port to preserve earlier takes"
            )
    items = [
        {
            "apiVersion": "v1",
            "kind": "ConfigMap",
            "metadata": {"name": fixture_name, "namespace": ns},
            "data": scripts,
        },
        {
            "apiVersion": "v1",
            "kind": "Service",
            "metadata": {"name": fixture_name, "namespace": ns},
            "spec": {
                "selector": {"lightspeed-demo-fixture": fixture_name},
                "ports": [
                    {"name": "http", "port": 8080},
                    {"name": "git", "port": 9418},
                ],
            },
        },
    ]
    if not existing:
        items.append(
            {
                "apiVersion": "v1",
                "kind": "Pod",
                "metadata": {
                    "name": fixture_name,
                    "namespace": ns,
                    "labels": {"lightspeed-demo-fixture": fixture_name},
                    "annotations": {"lightspeed-demo/source-sha256": source_hash},
                },
                "spec": {
                    "restartPolicy": "Never",
                    "securityContext": {"fsGroup": 1001},
                    "containers": [
                        {
                            "name": "fixture",
                            "image": args.fixture_image,
                            "imagePullPolicy": "IfNotPresent",
                            "command": [
                                "python3",
                                "/fixture/demo_fixture.py",
                                "--hostname",
                                hostname,
                            ],
                            "ports": [{"containerPort": 8080}, {"containerPort": 9418}],
                            "resources": {
                                "requests": {"cpu": "50m", "memory": "128Mi"},
                                "limits": {"memory": "512Mi"},
                            },
                            "volumeMounts": [
                                {"name": "scripts", "mountPath": "/fixture"},
                                {"name": "data", "mountPath": "/data"},
                            ],
                            "readinessProbe": {
                                "httpGet": {"path": "/health", "port": 8080},
                                "initialDelaySeconds": 1,
                                "periodSeconds": 2,
                            },
                        }
                    ],
                    "volumes": [
                        {"name": "scripts", "configMap": {"name": fixture_name}},
                        {"name": "data", "emptyDir": {"sizeLimit": "500Mi"}},
                    ],
                },
            }
        )
    apply(items)
    wait("pod", fixture_name, ready)
    try:
        with socket.create_connection(("127.0.0.1", args.control_port), timeout=0.2):
            health = http(control + "/health")
            if health.get("fixture") != "lightspeed-scripted-demo":
                raise RuntimeError("The control port belongs to another service")
    except OSError:
        log = (evidence / "fixture-port-forward.log").open("w")
        forward = subprocess.Popen(
            [
                "kubectl",
                "--kubeconfig",
                str(args.kubeconfig),
                "-n",
                ns,
                "port-forward",
                "svc/" + fixture_name,
                f"{args.control_port}:8080",
                "--address",
                "127.0.0.1",
            ],
            stdin=subprocess.DEVNULL,
            stdout=log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        (evidence / "fixture-port-forward.pid").write_text(str(forward.pid) + "\n")
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if forward.poll() is not None:
                raise RuntimeError("Fixture port-forward exited")
            try:
                if (
                    http(control + "/health").get("fixture")
                    == "lightspeed-scripted-demo"
                ):
                    break
            except OSError:
                time.sleep(0.1)
        else:
            raise TimeoutError("Fixture control endpoint did not open")
    session = http(control + "/sessions", {"name": args.session})
    app_name = "Lightspeed scripted demo " + args.session
    application = http(
        args.hub + "/applications",
        {
            "name": app_name,
            "description": "Scripted local model rehearsal; actual Lightspeed tools, runtime, user interaction, verification and Git",
            "repository": {
                "kind": "git",
                "url": session["repository"],
                "branch": "main",
            },
        },
    )
    gateway = resource(
        "Gateway",
        "scripted-local-model",
        {
            "provider": "openai",
            "endpoint": f"http://{hostname}:8080/sessions/{args.session}/v1",
            "credentialRef": {
                "secretName": "lightspeed-demo-provider",
                "key": "api-key",
            },
            "model": {"name": "fixture-model", "contextWindow": 128000},
        },
    )
    agent = resource(
        "Agent",
        "lightspeed-demo",
        {
            "image": args.image,
            "prompt": "Run the local scripted rehearsal. Ask the user for the target environment, inspect the actual runtime, and wait for new guidance before committing. Include the actual runtime details in your final reply.",
            "gateways": [{"ref": "scripted-local-model"}],
            "skillCards": [{"ref": "lightspeed-demo-house-rule"}],
            "execution": {"maxTurns": 20},
        },
    )
    agent["metadata"]["labels"] = {"konveyor.io/managed": "true"}
    apply(
        [
            {
                "apiVersion": "v1",
                "kind": "Secret",
                "metadata": {"name": "lightspeed-demo-provider", "namespace": ns},
                "stringData": {"api-key": "scripted-local-fixture-key"},
            },
            gateway,
            resource(
                "SkillCard",
                "lightspeed-demo-house-rule",
                {
                    "type": "rule",
                    "inline": "---\nname: lightspeed-demo-house-rule\ndescription: Rules for the local Lightspeed scripted rehearsal\n---\nUse only the local rehearsal repository. LIGHTSPEED_DEMO_RULE.\n",
                },
            ),
            agent,
        ]
    )
    wait("gateway", "scripted-local-model", ready)
    wait("agent", "lightspeed-demo", ready)
    setup = {
        "namespace": ns,
        "session": args.session,
        "scriptedModel": True,
        "applicationId": application["id"],
        "applicationName": app_name,
        "agent": "lightspeed-demo",
        "gateway": "scripted-local-model",
        "runtimeImage": args.image,
        "repository": session["repository"],
        "targetBranch": "lightspeed-demo-results",
        "mode": "approve",
        "askUser": True,
        "question": "Which environment should this rehearsal target?",
        "options": ["staging", "production"],
        "answer": "staging",
        "steeringText": "Use the canary rollout label.",
        "stateUrl": control + f"/sessions/{args.session}/state",
        "viewerReadyUrl": control + f"/sessions/{args.session}/viewer-ready",
        "releaseToolUrl": control + f"/sessions/{args.session}/release-tool",
        "gateMethod": "POST",
        "fixtureSourceSha256": source_hash,
        "fixturePod": fixture_name,
        "browserUI": "not yet verified by this setup helper",
    }
    (evidence / "setup.json").write_text(json.dumps(setup, indent=2) + "\n")
    print(json.dumps(setup, indent=2), flush=True)


if __name__ == "__main__":
    main()
