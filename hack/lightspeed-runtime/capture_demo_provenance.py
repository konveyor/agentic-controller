#!/usr/bin/env python3
"""Read-only runtime/image and outcome proof for an actual UI-created rehearsal.

Never creates runs, connects an ACP viewer, or opens fixture gates.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import time
from pathlib import Path
from urllib.request import urlopen

PROGRAM = """import importlib.metadata,lightspeed_agentic,inspect,pathlib,hashlib,json,os
from lightspeed_agentic.session import RuntimeSession
source=pathlib.Path(inspect.getsourcefile(RuntimeSession));processes=[]
for p in pathlib.Path('/proc').glob('[0-9]*/cmdline'):
 try:argv=p.read_bytes().decode().strip(chr(0)).split(chr(0))
 except (OSError,UnicodeError):continue
 if any(argv[i:i+2]==['-m','lightspeed_agentic.acp'] for i in range(len(argv)-1)):processes.append({'pid':int(p.parent.name),'python':argv[0],'module':'lightspeed_agentic.acp'})
print(json.dumps({'runtime':'lightspeed-agentic','version':importlib.metadata.version('lightspeed-agentic'),'module_path':lightspeed_agentic.__file__,'session_module_path':str(source),'session_source_sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'deepagents_version':importlib.metadata.version('deepagents'),'acp_processes':processes,'public_image_environment':{'HARNESS_RUNTIME':os.environ.get('HARNESS_RUNTIME'),'HARNESS_WORK_DIR':os.environ.get('HARNESS_WORK_DIR')}}))
"""


def http(url):
    with urlopen(url, timeout=5) as r:
        return json.load(r)


def save(directory, name, value):
    path = directory / name
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.replace(path)


def capture(setup, pod_name, tool, podman, kubeconfig, kind_node):
    kc = kubeconfig

    def kube(*args):
        return subprocess.check_output(
            ["kubectl", "--kubeconfig", str(kc), "-n", setup["namespace"], *args],
            text=True,
            stderr=subprocess.PIPE,
        )

    pod = json.loads(kube("get", "pod", pod_name, "-o", "json"))
    container = next(
        c["name"]
        for c in pod["spec"]["containers"]
        if c["name"] in ["agent", "runtime"]
    )
    imported = json.loads(
        kube("exec", pod_name, "-c", container, "--", "python3", "-c", PROGRAM)
    )
    image = next(
        c["image"] for c in pod["spec"]["containers"] if c["name"] == container
    )
    image_id = next(
        c["imageID"]
        for c in pod["status"]["containerStatuses"]
        if c["name"] == container
    )
    cri = json.loads(
        subprocess.check_output(
            [str(podman), "exec", kind_node, "crictl", "inspecti", image],
            text=True,
            stderr=subprocess.PIPE,
        )
    )
    labels = {
        k: v
        for k, v in cri["info"]["imageSpec"]["config"]["Labels"].items()
        if k
        in [
            "io.konveyor.lightspeed.revision",
            "io.konveyor.controller.revision",
            "io.buildah.version",
        ]
    }
    assert image_id.removeprefix("containerd://") in [
        cri["status"]["id"],
        *cri["status"].get("repoDigests", []),
    ], "Pod digest not associated with inspected tagged image"
    assert labels["io.konveyor.lightspeed.revision"] == setup["runtimeRevision"], (
        "Runtime image label mismatch"
    )
    assert labels["io.konveyor.controller.revision"] == setup["harnessRevision"], (
        "Harness image label mismatch"
    )
    assert cri["status"]["id"] == setup["runtimeConfigDigest"], (
        "Actual pod config digest mismatch"
    )
    assert imported["session_source_sha256"] == setup["expectedSessionSourceSha256"], (
        "Actual imported source mismatch"
    )
    assert imported["acp_processes"], "No actual Lightspeed ACP process observed"
    if tool is not None:
        for key in [
            "runtime",
            "version",
            "module_path",
            "session_module_path",
            "session_source_sha256",
            "deepagents_version",
            "acp_processes",
        ]:
            assert imported[key] == tool[key], (
                "Independent inspection differs from tool evidence: " + key
            )
    return {
        "pod": pod_name,
        "container": container,
        "podImage": image,
        "podImageID": image_id,
        "imageConfigDigest": cri["status"]["id"],
        "imageSourceLabels": labels,
        "independentlyImportedRuntime": imported,
        "podImageDigestAssociationVerified": True,
        "matchesActualToolEvidence": tool is not None,
        "matchesCommittedSessionSource": True,
        "browserActionsPerformedByThisWatcher": False,
        "scriptedModel": True,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("setup", type=Path)
    parser.add_argument("--kubeconfig", type=Path, required=True)
    parser.add_argument("--podman", type=Path, required=True)
    parser.add_argument("--runtime-dir", type=Path)
    parser.add_argument("--kind-node", default="lightspeed-runtime-control-plane")
    parser.add_argument("--hub", default="http://127.0.0.1:18081/hub")
    args = parser.parse_args()
    setup = json.loads(args.setup.read_text())
    directory = args.setup.resolve().parent
    if args.runtime_dir:
        cri = json.loads(
            subprocess.check_output(
                [
                    str(args.podman.resolve()),
                    "exec",
                    args.kind_node,
                    "crictl",
                    "inspecti",
                    setup["runtimeImage"],
                ],
                text=True,
                stderr=subprocess.PIPE,
            )
        )
        labels = cri["info"]["imageSpec"]["config"]["Labels"]
        revision = subprocess.check_output(
            ["git", "-C", str(args.runtime_dir), "rev-parse", "HEAD"], text=True
        ).strip()
        assert labels["io.konveyor.lightspeed.revision"] == revision, (
            "Selected runtime checkout differs from image source"
        )
        setup.update(
            {
                "runtimeRevision": revision,
                "harnessRevision": labels["io.konveyor.controller.revision"],
                "runtimeConfigDigest": cri["status"]["id"],
                "expectedSessionSourceSha256": hashlib.sha256(
                    subprocess.check_output(
                        [
                            "git",
                            "-C",
                            str(args.runtime_dir),
                            "show",
                            "HEAD:src/lightspeed_agentic/session.py",
                        ]
                    )
                ).hexdigest(),
            }
        )
    setup.setdefault(
        "expectedFinalUsage", {"inputTokens": 80, "outputTokens": 20, "turnsUsed": 4}
    )
    setup.setdefault("expectedClassifierCalls", 2)
    if (directory / "runtime-provenance-independent.json").exists():
        raise RuntimeError("Use a fresh take; independent proof already exists")
    print(
        "Observer ready; taskPodman="
        + str(args.podman.resolve())
        + "; lookup=tag + explicit pod digest association",
        flush=True,
    )
    deadline = time.monotonic() + 1800
    while time.monotonic() < deadline:
        try:
            state = http(setup["stateUrl"])
        except OSError as exc:
            print("Waiting for fixtureHTTP: " + type(exc).__name__, flush=True)
            time.sleep(1)
            continue
        if not state.get("provenanceReady"):
            time.sleep(0.1)
            continue
        runs = http(args.hub.rstrip("/") + "/agentic/agentruns")
        candidates = [
            r
            for r in runs
            if r["spec"].get("agentRef") == setup["agent"]
            and r["metadata"].get("labels", {}).get("konveyor.io/application")
            == str(setup["applicationId"])
        ]
        if not candidates:
            raise RuntimeError(
                "Provenance-ready fixture has no matching actual Hub AgentRun"
            )
        run = max(candidates, key=lambda r: r["metadata"]["creationTimestamp"])
        proof = capture(
            setup,
            run["status"]["sandboxName"],
            state["runtimeProvenance"],
            args.podman.resolve(),
            args.kubeconfig.resolve(),
            args.kind_node,
        )
        proof["agentRun"] = run["metadata"]["name"]
        save(directory, "runtime-provenance-independent.json", proof)
        print(
            "Captured independent provenance for " + run["metadata"]["name"], flush=True
        )
        break
    else:
        raise TimeoutError("No active provenance tool window observed")
    deadline = time.monotonic() + 300
    while time.monotonic() < deadline:
        final = http(
            args.hub.rstrip("/") + "/agentic/agentruns/" + run["metadata"]["name"]
        )
        if final.get("status", {}).get("phase") not in ["Succeeded", "Failed"]:
            time.sleep(0.5)
            continue
        state = http(setup["stateUrl"])
        save(directory, "fixture-final.json", state)
        save(
            directory,
            "agentrun-final.json",
            {
                "metadata": {
                    k: final["metadata"].get(k)
                    for k in ["name", "namespace", "uid", "creationTimestamp", "labels"]
                },
                "spec": {
                    k: final["spec"].get(k)
                    for k in ["agentRef", "gateway", "execution"]
                },
                "status": final["status"],
            },
        )
        term = final["status"].get("terminationData", {})
        term = json.loads(term) if isinstance(term, str) else term
        assert final["status"]["phase"] == "Succeeded", "Actual UI-created run failed"
        assert state["stage"] == "completed" and state["steeringSeen"], (
            "Steering decision sequence did not complete"
        )
        assert state["environment"] == "staging", "User target not applied"
        branch = state["pushedBranches"][setup["targetBranch"]]
        assert branch["commit"] == state["result"]["commit"], (
            "Pushed commit differs from actual tool output"
        )
        assert (
            branch["result"]["environment"] == "staging"
            and branch["result"]["rollout"] == "canary"
        ), "Pushed artifact differs from user choices"
        assert term.get("verification", {}).get("status") == "passed", (
            "Harness verification failed"
        )
        assert term["usage"] == setup["expectedFinalUsage"], (
            "Cumulative usage differs from scripted provider observations"
        )
        assert state["classifierCalls"] >= setup["expectedClassifierCalls"], (
            "Tool inspection missing"
        )
        summary = {
            "agentRun": run["metadata"]["name"],
            "phase": final["status"]["phase"],
            "verification": term["verification"]["status"],
            "usage": term["usage"],
            "environment": state["environment"],
            "rollout": "canary",
            "steeringObserved": True,
            "modelCalls": state["modelCalls"],
            "classifierCalls": state["classifierCalls"],
            "pushedCommit": branch["commit"],
            "independentRuntimeProvenanceVerified": True,
            "runtimeRevision": setup["runtimeRevision"],
            "harnessRevision": setup["harnessRevision"],
            "scriptedModel": True,
            "browserInteractionEvidence": "Owned and recorded separately by parent Playwright flow",
        }
        save(directory, "verification-summary.json", summary)
        print(json.dumps(summary), flush=True)
        return
    raise TimeoutError("Run did not finalize")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        import sys

        directory = Path(sys.argv[1]).resolve().parent
        details = {
            "type": type(error).__name__,
            "message": str(error),
            "stderr": getattr(error, "stderr", None),
        }
        save(directory, "observer-error.json", details)
        print(json.dumps(details), flush=True)
        raise
