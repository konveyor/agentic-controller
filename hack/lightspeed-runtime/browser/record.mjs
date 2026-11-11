import assert from "node:assert/strict";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { chromium } from "playwright";

assert(process.argv[2], "Usage: node record.mjs /path/to/setup.json");
const setup = JSON.parse(await readFile(process.argv[2], "utf8"));
const baseURL = (process.env.DEMO_URL ?? "http://127.0.0.1:18081").replace(/\/$/, "");
const output = path.resolve(process.env.DEMO_OUTPUT ?? `artifacts/${setup.session}`);
const pace = Number(process.env.DEMO_PACE_MS ?? "1500");
assert(Number.isFinite(pace) && pace >= 0 && pace <= 10000, "Invalid DEMO_PACE_MS");
assert.equal(setup.scriptedModel, true, "A scripted local fixture is required");
for (const key of ["stateUrl", "viewerReadyUrl", "releaseToolUrl"]) {
  assert(["127.0.0.1", "localhost", "[::1]"].includes(new URL(setup[key]).hostname),
    `Fixture ${key} must use loopback`);
}
await mkdir(path.dirname(output), { recursive: true });
await mkdir(output); // Keep failed takes and their evidence intact.

const safeURL = (raw) => {
  try { const u = new URL(raw); return `${u.protocol}//${u.host}${u.pathname}`; }
  catch { return "[invalid URL]"; }
};
const sanitize = (v) => String(v)
  .replace(/(?:https?|wss?):\/\/[^\s"'<>]+/g, safeURL)
  .replace(/(["']?(?:nonce|token|secret(?:[_-]?key)?|authorization|api[_-]?key)["']?\s*[=:]\s*)["']?[^\s,;}"']+/gi, "$1[redacted]");
const evidence = {
  success: false, scriptedModel: true, startedAt: new Date().toISOString(),
  setup: Object.fromEntries(["session", "agent", "gateway", "applicationId", "targetBranch",
    "runtimeImage", "runtimeRevision", "harnessRevision", "runtimeConfigDigest"]
    .map((key) => [key, setup[key]])),
  steps: [], pageErrors: [], consoleErrors: [], failedRequests: [], badResponses: [], phases: [],
  acp: { sockets: [], incomingMethods: {}, outgoingMethods: {}, viewerSessions: [],
    steering: [], answers: [], usage: [] },
};
let browser, context, page, runName;
const requests = new Map(), asks = new Map(), steers = new Map();
const now = () => new Date().toISOString();
const add = (list, value) => { if (list.length < 100) list.push(value); };
function observeFrame(direction, payload) {
  let frame;
  try { frame = JSON.parse(typeof payload === "string" ? payload : payload.toString("utf8")); }
  catch { return; }
  if (!frame || typeof frame !== "object") return;
  const method = frame.method;
  if (typeof method === "string") {
    const counts = evidence.acp[direction === "in" ? "incomingMethods" : "outgoingMethods"];
    counts[method] = (counts[method] ?? 0) + 1;
    if (direction === "out" && ["session/new", "session/load"].includes(method)) {
      const entry = { at: now(), method, cwd: frame.params?.cwd, status: "sent" };
      evidence.acp.viewerSessions.push(entry); requests.set(frame.id, entry);
    }
    if (direction === "out" && method === "_goose/unstable/session/steer") {
      const entry = { at: now(), status: "sent", sessionId: frame.params?.sessionId,
        expectedRunId: frame.params?.expectedRunId,
        text: sanitize(frame.params?.prompt?.filter((b) => b.type === "text").map((b) => b.text).join("\n") ?? "") };
      evidence.acp.steering.push(entry); steers.set(frame.id, entry);
    }
    if (direction === "in" && ["session/request_permission", "elicitation/create"].includes(method)) asks.set(frame.id, method);
    const u = frame.params?.update;
    if (direction === "in" && u?.sessionUpdate === "usage_update") {
      const snapshot = { at: now(), method, sessionId: frame.params?.sessionId };
      for (const key of ["inputTokens", "outputTokens", "accumulatedInputTokens", "accumulatedOutputTokens"]) {
        if (typeof u[key] === "number") snapshot[key] = u[key];
      }
      if (u.cost !== undefined) snapshot.cost = { amount: u.cost?.amount, currency: u.cost?.currency };
      add(evidence.acp.usage, snapshot);
    }
  } else if (direction === "in" && requests.has(frame.id)) {
    const entry = requests.get(frame.id);
    entry.status = frame.error ? "rejected" : "created";
    if (frame.result?.sessionId) entry.sessionId = frame.result.sessionId;
    if (frame.error) entry.errorCode = frame.error.code;
    requests.delete(frame.id);
  } else if (direction === "in" && steers.has(frame.id)) {
    const entry = steers.get(frame.id);
    entry.status = frame.result?.messageId ? "accepted" : "rejected";
    if (frame.result?.messageId) entry.messageId = frame.result.messageId;
    if (frame.result?.runId) entry.runId = frame.result.runId;
    if (frame.error) entry.errorCode = frame.error.code;
    steers.delete(frame.id);
  } else if (direction === "out" && asks.has(frame.id)) {
    const result = frame.result ?? {};
    evidence.acp.answers.push({ at: now(), method: asks.get(frame.id), action: result.action,
      answer: result.content?.answer && sanitize(result.content.answer),
      outcome: result.outcome && { outcome: result.outcome.outcome, optionId: result.outcome.optionId } });
    asks.delete(frame.id);
  }
}

async function jsonRequest(url, method = "GET") {
  const r = await context.request.fetch(url, { method, timeout: 10000 });
  assert(r.ok(), `${method} ${safeURL(url)} returned ${r.status()}`);
  return r.json();
}
async function fixtureState() {
  const state = await jsonRequest(setup.stateUrl);
  assert.equal(state.scriptedModel, true);
  assert(!state.failure, `Fixture failed: ${sanitize(state.failure)}`);
  return state;
}
async function until(label, predicate, timeout = 120000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const result = await predicate(); if (result) return result;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error(`Timed out waiting for ${label}`);
}
async function shot(name, hold = pace) {
  await page.screenshot({ path: path.join(output, `${name}.png`) });
  evidence.steps.push({ at: now(), name, url: safeURL(page.url()) });
  await page.waitForTimeout(hold);
}
async function finish(operation, label) {
  let timer;
  try {
    await Promise.race([operation, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${label} timed out`)), 30000);
      timer.unref();
    })]);
  } finally { clearTimeout(timer); }
}

function runSummary(run) {
  const s = run.status ?? {};
  let t = s.terminationData ?? {};
  if (typeof t === "string") { try { t = JSON.parse(t); } catch { t = {}; } }
  return { name: run.metadata?.name, phase: s.phase,
    conditions: (s.conditions ?? []).map(({ type, status, reason }) => ({ type, status, reason })),
    termination: { exitCode: t.exitCode, outcome: t.outcome, stopReason: t.stopReason,
      usage: t.usage && Object.fromEntries(["inputTokens", "outputTokens", "turnsUsed", "cost",
        "contextUsed", "contextSize"].filter((key) => t.usage[key] !== undefined)
        .map((key) => [key, t.usage[key]])), verification: t.verification && {
        status: t.verification.status, commit: t.verification.commit } } };
}
async function readRun() {
  const run = await jsonRequest(`${baseURL}/hub/agentic/agentruns/${encodeURIComponent(runName)}`);
  const summary = runSummary(run);
  if (evidence.phases.at(-1)?.phase !== summary.phase) evidence.phases.push({ at: now(), ...summary });
  evidence.run = summary; return run;
}
function fixtureSummary(s) {
  const p = s.runtimeProvenance;
  return { name: s.name, stage: s.stage, modelCalls: s.modelCalls, classifierCalls: s.classifierCalls,
    environment: s.environment, steeringSeen: s.steeringSeen, guidance: sanitize(s.guidance ?? ""),
    viewerGateOpen: s.viewerGateOpen, toolGateOpen: s.toolGateOpen, provenanceReady: s.provenanceReady,
    runtimeProvenance: p && { runtime: p.runtime, version: p.version, modulePath: p.module_path,
      sessionModulePath: p.session_module_path, sessionSourceSha256: p.session_source_sha256,
      deepagentsVersion: p.deepagents_version, pythonVersion: p.python_version,
      acpProcesses: p.acp_processes, harnessCredentialsInEnvironment: p.harness_credentials_in_environment },
    result: s.result && { environment: s.result.environment, rollout: s.result.rollout, commit: s.result.commit },
    pushedBranches: Object.fromEntries(Object.entries(s.pushedBranches ?? {}).map(([name, b]) =>
      [name, { commit: b.commit, environment: b.result?.environment, rollout: b.result?.rollout }])) };
}
async function independentProof() {
  if (!process.env.DEMO_PROVENANCE_FILE) return;
  const proof = await until("independent runtime provenance", async () => {
    let p;
    try { p = JSON.parse(await readFile(process.env.DEMO_PROVENANCE_FILE, "utf8")); }
    catch (error) { if (error.code === "ENOENT" || error instanceof SyntaxError) return false; throw error; }
    assert.equal(p.agentRun, runName, "Independent evidence belongs to another AgentRun");
    assert.equal(p.matchesActualToolEvidence, true, "Independent process evidence did not match");
    assert.equal(p.matchesCommittedSessionSource, true, "Independent source verification did not match");
    return p;
  }, 30000);
  evidence.independentProvenance = { matchesActualToolEvidence: proof.matchesActualToolEvidence,
    matchesCommittedSessionSource: proof.matchesCommittedSessionSource,
    artifact: path.basename(process.env.DEMO_PROVENANCE_FILE) };
}

try {
  browser = await chromium.launch({ headless: true,
    executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE });
  context = await browser.newContext({ viewport: { width: 1600, height: 900 },
    recordVideo: { dir: output, size: { width: 1600, height: 900 } } });
  page = await context.newPage(); page.setDefaultTimeout(30000);
  page.on("pageerror", (error) => add(evidence.pageErrors, sanitize(error.message)));
  page.on("console", (message) => {
    if (message.type() === "error") add(evidence.consoleErrors, sanitize(message.text()).slice(0, 1500));
  });
  page.on("requestfailed", (r) => add(evidence.failedRequests,
    { url: safeURL(r.url()), error: sanitize(r.failure()?.errorText) }));
  page.on("response", (r) => {
    if (r.status() >= 400) add(evidence.badResponses, { url: safeURL(r.url()), status: r.status() });
  });
  page.on("websocket", (socket) => {
    evidence.acp.sockets.push({ at: now(), url: safeURL(socket.url()) });
    socket.on("framereceived", ({ payload }) => observeFrame("in", payload));
    socket.on("framesent", ({ payload }) => observeFrame("out", payload));
  });
  const before = await fixtureState();
  assert.equal(before.modelCalls, 0, "Use a fresh fixture: model calls already started");
  assert.equal(before.viewerGateOpen, false, "Use a fresh unopened viewer gate");
  assert.equal(before.toolGateOpen, false, "Use a fresh unopened tool gate");
  assert.equal(Object.keys(before.pushedBranches ?? {}).length, 0, "Use a fresh repository fixture");
  evidence.preflight = fixtureSummary(before);

  await page.goto(`${baseURL}/applications`, { waitUntil: "domcontentloaded", timeout: 45000 });
  await page.getByRole("link", { name: "Agents", exact: true }).click();
  const row = page.getByRole("row").filter({ hasText: setup.agent });
  await row.waitFor({ state: "visible" });
  const agentText = await row.innerText();
  assert(agentText.includes("agent-lightspeed"), "Agent row lacks the Lightspeed image");
  assert(agentText.includes(setup.gateway), "Agent row lacks the scripted Gateway");
  evidence.agentRow = sanitize(agentText);
  await shot("01-lightspeed-agent");
  await page.getByRole("link", { name: "Agent runs", exact: true }).click();
  await page.getByRole("button", { name: "Create run", exact: true }).click();
  await page.locator("#create-agent").selectOption(setup.agent);
  await page.locator("#create-gateway").selectOption(setup.gateway);
  await page.locator("#create-application").selectOption(String(setup.applicationId));
  await page.locator("#create-run-target-branch").fill(setup.targetBranch);
  await page.locator("#create-run-execution-mode").selectOption("approve");
  await page.locator("#create-run-ask-user").check();
  await page.locator("#create-instructions").fill(
    "Scripted Lightspeed runtime probe using local model, Git and tools. Ask for the target environment, preserve human steering, and commit the verified result.");
  await shot("02-create-scripted-run");
  await page.locator("#create-run-submit").click();
  await page.waitForURL(/\/agent-runs\/[^/?#]+(?:[?#].*)?$/, { timeout: 45000 });
  runName = new URL(page.url()).pathname.split("/").filter(Boolean).at(-1);
  assert(runName && runName !== "agent-runs", "No actual AgentRun URL after creation");

  await until("actual UI session/new success", () => evidence.acp.viewerSessions.find((e) =>
    e.method === "session/new" && e.cwd === "/workspace" && e.status === "created"));
  await until("Hub ACPReady", async () => {
    const run = await readRun();
    assert(run.status?.phase !== "Failed", "AgentRun failed before ACP became ready");
    return run.status?.conditions?.some((c) => c.type === "ACPReady" && c.status === "True");
  });
  await jsonRequest(setup.viewerReadyUrl, "POST"); evidence.viewerGateOpenedAt = now();

  const question = page.locator(".chat-ask").filter({ hasText: setup.question });
  await question.waitFor({ state: "visible", timeout: 45000 });
  const expand = page.getByRole("button", { name: "Expand the transcript to full screen", exact: true });
  if (await expand.isVisible()) await expand.click();
  await question.getByRole("radio", { name: setup.answer ?? "staging", exact: true }).check();
  await shot("03-human-question");
  await question.getByRole("button", { name: "Answer", exact: true }).click();

  const allow = page.getByRole("button", { name: "Allow once", exact: true });
  await allow.waitFor({ state: "visible", timeout: 45000 });
  await shot("04-approve-runtime-inspection"); await allow.click();
  await until("runtime provenance and active tool gate", async () => {
    const s = await fixtureState(); return s.provenanceReady && s;
  }, 45000);
  const usage = page.locator('[aria-label="Usage"]');
  await usage.waitFor({ state: "visible", timeout: 15000 });
  await until("visible live 40 input / 10 output tokens", async () => {
    const text = await usage.innerText();
    return /\b40\s*in\b/.test(text) && /\b10\s*out\b/.test(text);
  }, 10000);
  evidence.liveUsageVisible = true; evidence.liveUsageText = sanitize(await usage.innerText());
  const composer = page.getByPlaceholder(/^Redirect the agent/);
  await composer.fill(setup.steeringText); await composer.press("Enter");
  await until("accepted UI steering response", () => evidence.acp.steering.find((e) =>
    e.status === "accepted" && e.text === setup.steeringText), 15000);
  await page.getByText("queued — the agent reads it at its next step", { exact: true })
    .waitFor({ state: "visible", timeout: 10000 });
  await shot("05-guidance-queued-live-usage");
  await independentProof();
  await jsonRequest(setup.releaseToolUrl, "POST"); evidence.toolGateReleasedAt = now();

  await allow.waitFor({ state: "visible", timeout: 45000 });
  await shot("06-approve-verified-commit"); await allow.click();
  await until("final cumulative usage", () => evidence.acp.usage.find((u) =>
    u.inputTokens === (setup.expectedFinalUsage?.inputTokens ?? 80) &&
    u.outputTokens === (setup.expectedFinalUsage?.outputTokens ?? 20)), 60000);
  await until("Succeeded and Verified", async () => {
    const run = await readRun();
    assert(run.status?.phase !== "Failed", "AgentRun failed after UI interaction");
    return run.status?.phase === "Succeeded" && run.status?.conditions?.some((c) =>
      c.type === "Verified" && c.status === "True") && run;
  });
  const state = await until("actual pushed branch", async () => {
    const s = await fixtureState(); return s.pushedBranches?.[setup.targetBranch] && s;
  });
  assert.equal(state.environment, "staging"); assert.equal(state.steeringSeen, true);
  assert.equal(state.runtimeProvenance?.runtime, "lightspeed-agentic");
  assert.equal(state.runtimeProvenance?.harness_credentials_in_environment, false);
  assert(state.runtimeProvenance?.acp_processes?.some((p) => p.module === "lightspeed_agentic.acp"));
  if (setup.expectedSessionSourceSha256) {
    assert.equal(state.runtimeProvenance?.session_source_sha256, setup.expectedSessionSourceSha256);
  }
  assert.equal(state.pushedBranches[setup.targetBranch].commit, state.result?.commit);
  assert.equal(state.pushedBranches[setup.targetBranch].result?.rollout, "canary");
  assert.equal(state.classifierCalls, setup.expectedClassifierCalls ?? 2);
  assert.equal(evidence.run.termination.verification?.status, "passed");
  evidence.fixture = fixtureSummary(state);

  await usage.hover();
  const tooltip = page.getByRole("tooltip"); await tooltip.waitFor({ state: "visible", timeout: 5000 });
  evidence.finalUsageText = sanitize(`${await usage.innerText()} ${await tooltip.innerText()}`);
  assert(/\b80\b/.test(evidence.finalUsageText) && /\b20\b/.test(evidence.finalUsageText),
    "UI does not visibly show final 80 input / 20 output tokens");
  assert(!/\$\s*0(?:\.00*)?\b/.test(evidence.finalUsageText), "Unknown cost displayed as zero dollars");
  assert((evidence.acp.incomingMethods["_lightspeed/session/update"] ?? 0) > 0);
  assert((evidence.acp.incomingMethods["session/request_permission"] ?? 0) >= 2);
  assert((evidence.acp.incomingMethods["elicitation/create"] ?? 0) >= 1);
  assert.equal(evidence.pageErrors.length, 0, "Browser page errors occurred");
  await shot("07-final-live-token-usage");
  const collapse = page.getByRole("button", { name: "Exit full screen (Esc)", exact: true });
  if (await collapse.isVisible()) await collapse.click();
  const phase = page.locator(".run-phase-line").filter({ hasText: "Succeeded" });
  await phase.waitFor({ state: "visible" });
  await phase.scrollIntoViewIfNeeded();
  await shot("08-succeeded"); // Verified is checked through Hub; the UI does not render it.
  if (await expand.isVisible()) await expand.click();
  await page.getByText(/Applied your decisions: staging target, canary rollout/).waitFor({ state: "visible" });
  await page.getByText(/Actual runtime: lightspeed-agentic/).scrollIntoViewIfNeeded();
  await shot("09-actual-runtime-and-commit", Math.max(pace, 6000));
  evidence.success = true;
} catch (error) {
  evidence.failure = sanitize(error.stack ?? error.message).slice(0, 8000);
  if (page && !page.isClosed()) {
    evidence.failureBody = sanitize(await page.locator("body").innerText().catch(() => "")).slice(0, 20000);
    await page.screenshot({ path: path.join(output, "failure.png"), timeout: 5000 }).catch(() => {});
  }
} finally {
  if (context) {
    if (!evidence.fixture) {
      try { evidence.fixture = fixtureSummary(await jsonRequest(setup.stateUrl)); } catch { /* keep original failure */ }
    }
    const video = page?.video();
    try { await finish(context.close(), "Video/context close"); if (video) evidence.video = path.basename(await video.path()); }
    catch (error) { evidence.success = false; evidence.videoFailure = sanitize(error.message); }
  }
  try { if (browser) await finish(browser.close(), "Browser close"); }
  catch (error) { evidence.success = false; evidence.browserCloseFailure = sanitize(error.message); }
  evidence.finishedAt = now();
  await writeFile(path.join(output, "evidence.json"), JSON.stringify(evidence, null, 2) + "\n");
  console.log(JSON.stringify({ success: evidence.success, runName, output,
    evidence: path.join(output, "evidence.json"), video: evidence.video }));
}
if (!evidence.success) process.exitCode = 1;
