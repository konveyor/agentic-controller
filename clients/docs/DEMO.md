# Demo: the agentic platform, end to end (~12 minutes)

What this shows: **this repository's controller doing real work.** A
browser UI and the VSCode extension drive the same AgentRun/ACP contract,
a real goose+Bedrock agent reads a real repository, and a run started in
the web UI is picked up from the IDE — the architect → developer handoff.

No simulator anywhere: the reconciler is the real controller, the sandbox
is Agent Sandbox v0.5.0, the agent is goose 1.39 on AWS Bedrock.

All paths below are relative to `clients/` in this repository.

## The stack

| Piece | What | Where |
|---|---|---|
| minikube | konveyor CRDs, Agent Sandbox v0.5.0, **agentic-controller** | this repo: `make docker-build deploy IMG=agentic-controller:dev` |
| `packages/hub-shim` | stand-in for the future Hub passthrough proxy (REST + WS `/acp`, issue #21) | :7080 |
| `ui/` | browser SPA (PatternFly) | :5199 |
| extension | `editor-extensions-cluster-agent` branch `feature/cluster-agent` | F5 dev host |
| `goose-harness:dev` | real agent base: entrypoint clones the repo, maps model env, runs `goose serve` | built into minikube (auto-rebuilt if missing) |
| `acp-mock-harness:dev` | deterministic mock agent (no LLM) — for the create-flow beat | built into minikube (auto-rebuilt if missing) |

**One command** (idempotent; safe after reboot / `minikube stop`):

```sh
hack/demo-up.sh     # preflight + converge cluster + start shim & UI, prints URLs
hack/demo-check.sh  # pre-demo smoke: full ACP round-trip on a throwaway mock run
hack/demo-down.sh   # stop the local processes; cluster untouched
```

Run `demo-check.sh` right before presenting — exit 0 means every beat's
surface is live (it creates, chats with, replays, and deletes a mock run
through the same shim/WS path the browser uses; costs nothing).

It verifies the controller deployment, rebuilds missing harness images,
applies `manifests/samples.yaml` (+ `goose-bedrock.yaml` when
`aws-bedrock-creds` exists), and gates on the Agent going Ready. Only true
prerequisites: minikube running, Agent Sandbox installed, and the
controller deployed from this repo (see the stack table). Runs do not
survive a minikube restart
(`restartPolicy: Never`) — create fresh ones, don't rely on old pods.

## Beat 1 — create a run in the browser (2 min)

Open http://localhost:5199 → **Create run** → agent `migration-analyzer`
(the mock — instant, free) → application **Coolstore** → Create.

Note there is no repository field to type into: the agent declares that its
`repository`, `branch`, and git credentials come from the application
(ADR 0005), so the form shows what the platform will resolve and asks only
for what a human should actually answer. Worth saying out loud — it is the
whole point of the beat.

Narrate what's happening live: the SPA POSTs to the shim, the shim creates
the AgentRun CR, the **real controller** validates the Agent/provider chain
and creates a Sandbox CR, Agent Sandbox spins the pod, the phase label
flips Pending → Running, and the chat auto-connects over WebSocket through
the shim (X-Secret-Key injected server-side — a browser can't set WS
headers, which is exactly why the Hub proxy seat exists).

Send a message; show streamed chunks + the tool-call card. Delete the run
from the kebab (cascade: Sandbox, pod, Service, secret all GC).

## Beat 2 — the real agent (goose + Bedrock) (4 min)

The UI's create form doesn't yet set `models:`/`envFrom:` (known gap), so
create the real run via the API — which is the point: same CR, any client:

```sh
kubectl create -f docs/demo/real-run.yaml
kubectl logs -f -n konveyor-agents -l agents.x-k8s.io/sandbox-name-hash --tail=50   # or: kubectl logs <run-name>
```

Show the agent-base log lines: `cloning …coolstore@main`, `clone OK: … pom.xml …`,
`provider=aws_bedrock model=…haiku…`. That's the Phase-4 agent base doing
what the controller deliberately doesn't.

Open the run in the browser UI and ask:

> What build system does this project use? Name two files at the repository
> root that support your answer.

Watch a **real tool call** (`tree /workspace`) and a grounded answer
(Maven, `pom.xml`). This is Claude on Bedrock reading the actual clone.

## Beat 3 — the handoff (3 min)

F5 the extension dev host with the **coolstore** workspace open.

Within ~15s: a toast — *"Cluster agent real-XXXXX is running on this
workspace's repo (main). Attach to it?"* — the extension matched the run's
`repository` param against the workspace's git remote.

Click **Attach**: the session history **replays** (`session/load`),
including the browser-side Q&A, and the conversation continues in the IDE
next to the actual code. Same run, same session, two shells.

Also worth showing: `Konveyor: Attach to Cluster Agent for This Workspace`
in the palette (on-demand version of the toast).

## Beat 4 — pull the plug (2 min)

Failure handling, live. Create a fresh mock run (Beat 1 flow) and send:

> connectivity drill: TEST_DROP

The mock harness streams "about to lose connectivity", then destroys every
TCP connection it holds — pod-side death, mid-turn, prompt still pending.

What the audience sees: within a second or two the chat flips to
**"Disconnected from the agent"** with a Reconnect button. Worth saying
what *didn't* happen: before the connection-death fix this exact prompt
hung the session forever — the port-forward tunnel swallowed the pod-side
teardown, so the browser stayed "connected" with a spinner and no error.
Now the tunnel mirrors the teardown onto the browser socket (clean 1011
close in ~1s), with a ping/pong keepalive (`ACP_KEEPALIVE_MS`) as the
backstop for any silent-death mode the teardown misses.

Click **Reconnect**: the pod is still alive (only its sockets died), so
the session reloads and the full history replays — the same `session/load`
path as Beat 3's handoff. Drop surfaced, drop recovered.

Pre-verifiable like the rest: `npm run drop-check` in `packages/hub-shim`
(`dev/drop-check.ts`) runs this scenario end to end — throwaway mock run,
TEST_DROP prompt, browser-side 1011 required within budget — plus a
no-cluster direct-dial check.

## Talking points

- **Only one lane changes later**: browser clients (this SPA, tackle2-ui,
  RHDH) all ride the gateway seat; hub-shim occupies it today, the real Hub
  passthrough proxy replaces it — the shim's route table *is* the proposed
  spec (`docs/adr/0004`, repo root). Hub already has the `/services/:name/*path`
  precedent; stdlib ReverseProxy has done WS upgrades since Go 1.12.
- **Nobody rewrites UX**: the extension kept its panel/tree; tackle2-ui
  gains chat capability it doesn't have (zero WS code today).
- **Contract is verified, not aspirational**: pod == `status.sandboxName`,
  ACP key `secret-key`, headless portless Service, no run label on the pod,
  whole-spec immutability — all proven against the live controller and
  encoded in the shared client core.
- **Controller gaps this stack surfaced are upstreamed**: CRD CEL fixes and
  the scheme-builder cleanup are merged; sandbox pod run-labels and
  multi-key (SigV4) provider credentials are proposed as a follow-up PR.

## Cleanup

```sh
kubectl delete agentrun --field-selector metadata.name=<run> -n konveyor-agents  # or by name
```

Idle goose costs nothing (Bedrock bills per request), so keeping the real
run alive as a standing demo target is fine.
