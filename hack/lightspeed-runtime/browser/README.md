# Record the Lightspeed runtime integration

This Playwright recording drives the real Tackle UI and Hub. It selects the
Lightspeed Agent, creates an AgentRun, answers a question, approves tools, sends
guidance during execution, and checks the displayed usage and successful result.
The Agent's image is shown before execution. A tool inspects the actual imported
Lightspeed package, session source hash, DeepAgents version, and ACP process.

The recording uses an explicitly **scripted local model** for repeatable timing.
Model decisions are scripted; the browser, Hub, controller, Agent Sandbox,
runtime, tools, inspection, verification, and Git operations are real. This
demonstrates integration behavior, not a live model's reasoning quality.

Use a disposable local installation with the patched UI and runtime image,
Agent Sandbox, the controller, and Hub. Enable the UI's agentic and steering
features. The setup helper creates dedicated demo resources and a new Hub
application but does not start an AgentRun.

From the controller checkout, prepare a fresh take (use a new session and
evidence directory each time):

```sh
python3 hack/lightspeed-runtime/prepare_demo.py \
  --kubeconfig /path/to/task.kubeconfig \
  --namespace lightspeed-konveyor \
  --image localhost/agent-lightspeed:demo \
  --hub http://127.0.0.1:18081/hub \
  --session demo-take-01 \
  --evidence .scratch/demo-take-01

cd hack/lightspeed-runtime/browser
npm ci
npx playwright install chromium
DEMO_OUTPUT=/absolute/path/to/recording-take-01 \
  node record.mjs /absolute/path/to/.scratch/demo-take-01/setup.json
```

`DEMO_URL` defaults to `http://127.0.0.1:18081`. `DEMO_PACE_MS` defaults to
1500 milliseconds between key views. `PLAYWRIGHT_CHROMIUM_EXECUTABLE` can point
to an explicitly installed Chromium binary; `PLAYWRIGHT_BROWSERS_PATH` follows
Playwright's normal browser-cache convention.

The output includes the raw WebM screen recording, screenshots, and
`evidence.json`. The driver checks the UI's successful session handshake,
permission/question replies, accepted steering, token updates, Hub status,
independent harness verification, and the actual pushed Git result. It records
sanitized protocol summaries; WebSocket nonce query strings and credential
values must not be published. A failed take retains its diagnostic artifacts
and exits nonzero.

An optional `DEMO_PROVENANCE_FILE` can name an independent active-pod inspection
artifact. When set, the driver holds the fixture's tool gate until that artifact
confirms both `matchesActualToolEvidence` and `matchesCommittedSessionSource`.
It also requires the artifact to name the actual UI-created AgentRun.
The recorder itself never sends an ACP answer or steering request outside the
actual UI; the fixture control endpoints only coordinate timing.

To capture that independent proof on a Podman-backed Kind cluster, start the
read-only observer from the controller checkout in another terminal before
starting the recorder:

```sh
python3 hack/lightspeed-runtime/capture_demo_provenance.py \
  /absolute/path/to/.scratch/demo-take-01/setup.json \
  --kubeconfig /path/to/task.kubeconfig \
  --podman /absolute/path/to/task-podman \
  --runtime-dir /path/to/lightspeed-agentic-sandbox \
  --kind-node lightspeed-runtime-control-plane \
  --hub http://127.0.0.1:18081/hub
```

Use the Podman executable or wrapper for the intended local cluster. The observer
checks the imported runtime, active ACP process, committed session source, image
digest and source labels. It then checks the terminal result and pushed branch.
It does not create a run or interact with the viewer. Set
`DEMO_PROVENANCE_FILE=/absolute/path/to/.scratch/demo-take-01/runtime-provenance-independent.json`
when launching `record.mjs`. Each observer and recording needs a fresh take;
existing proof and recording directories are preserved.

For broad presentation-player support, convert the successful WebM to MP4:

```sh
ffmpeg -i /path/to/recording.webm -c:v libx264 -crf 18 \
  -pix_fmt yuv420p -movflags +faststart /path/to/demo.mp4
```

`npm run inspect` captures initial rendering and console/network errors without
creating a run. Keep raw diagnostic artifacts in ignored local directories.
