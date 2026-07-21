/**
 * Pure unit checks — no network, no cluster. Run: npx tsx dev/selfcheck.ts
 *
 * Covers resolveSecretKeyFromData (both key names, precedence, sole-entry
 * fallback, error paths, utf-8), ShimClient.acpUrl derivation,
 * isTerminalPhase, and waitForRunning against an in-memory fake RunApi.
 */
import {
  isTerminalPhase,
  resolveSecretKeyFromData,
  waitForRunning,
  type AgentRun,
  type RunApi,
} from "../src/contract/index.js";
import { ShimClient } from "../src/transport-shim/index.js";

let passed = 0;
let failed = 0;

function check(name: string, fn: () => void | Promise<void>): Promise<void> {
  return Promise.resolve()
    .then(fn)
    .then(() => {
      passed++;
      console.log(`  ok    ${name}`);
    })
    .catch((err: unknown) => {
      failed++;
      console.error(`  FAIL  ${name}: ${err instanceof Error ? err.message : String(err)}`);
    });
}

function assertEqual(actual: unknown, expected: unknown, label = "value"): void {
  if (actual !== expected) {
    throw new Error(`${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
  }
}

async function assertRejects(p: Promise<unknown>, contains: string[]): Promise<void> {
  try {
    await p;
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    for (const fragment of contains) {
      if (!msg.includes(fragment)) {
        throw new Error(`rejection message missing "${fragment}": ${msg}`);
      }
    }
    return;
  }
  throw new Error("expected rejection, but promise resolved");
}

function assertThrows(fn: () => unknown, contains: string[]): void {
  try {
    fn();
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    for (const fragment of contains) {
      if (!msg.includes(fragment)) {
        throw new Error(`error message missing "${fragment}": ${msg}`);
      }
    }
    return;
  }
  throw new Error("expected throw, but function returned");
}

/** base64 of a utf-8 string, browser-style (no Buffer). */
function b64(s: string): string {
  const bytes = new TextEncoder().encode(s);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}

async function main(): Promise<void> {
  console.log("resolveSecretKeyFromData");
  await check("decodes 'secret-key' (real controller)", () => {
    assertEqual(resolveSecretKeyFromData({ "secret-key": b64("real-key-123") }), "real-key-123");
  });
  await check("decodes 'ACP_SECRET_KEY' (legacy simulator)", () => {
    assertEqual(resolveSecretKeyFromData({ ACP_SECRET_KEY: b64("legacy-key") }), "legacy-key");
  });
  await check("prefers 'secret-key' when both present", () => {
    assertEqual(
      resolveSecretKeyFromData({
        ACP_SECRET_KEY: b64("legacy"),
        "secret-key": b64("preferred"),
      }),
      "preferred",
    );
  });
  await check("sole-entry fallback for unknown key names", () => {
    assertEqual(resolveSecretKeyFromData({ "some-custom-name": b64("solo-key") }), "solo-key");
  });
  await check("decodes multibyte utf-8", () => {
    assertEqual(resolveSecretKeyFromData({ "secret-key": b64("clé-🔑-秘密") }), "clé-🔑-秘密");
  });
  await check("throws on empty data, listing looked-for keys", () => {
    assertThrows(() => resolveSecretKeyFromData({}), ["secret-key", "ACP_SECRET_KEY", "no data entries"]);
  });
  await check("throws on multiple unknown keys, listing keys present", () => {
    assertThrows(
      () => resolveSecretKeyFromData({ alpha: b64("a"), beta: b64("b") }),
      ["alpha", "beta", "sole-entry"],
    );
  });
  await check("throws descriptively on invalid base64", () => {
    assertThrows(() => resolveSecretKeyFromData({ "secret-key": "!!!not-base64!!!" }), [
      "secret-key",
      "base64",
    ]);
  });

  console.log("ShimClient.acpUrl");
  await check("http -> ws with port", () => {
    assertEqual(
      new ShimClient("http://127.0.0.1:7080").acpUrl("ui-abc12"),
      "ws://127.0.0.1:7080/api/agentruns/ui-abc12/acp",
    );
  });
  await check("https -> wss, trailing slash + path prefix preserved", () => {
    assertEqual(
      new ShimClient("https://hub.example.com/shim/").acpUrl("run-1"),
      "wss://hub.example.com/shim/api/agentruns/run-1/acp",
    );
  });
  await check("run name is URL-encoded", () => {
    assertEqual(
      new ShimClient("http://localhost:7080").acpUrl("we ird"),
      "ws://localhost:7080/api/agentruns/we%20ird/acp",
    );
  });
  await check("rejects non-http(s) baseUrl", () => {
    assertThrows(() => new ShimClient("ftp://nope"), ["http(s)"]);
  });

  console.log("isTerminalPhase");
  await check("Succeeded/Failed terminal; Pending/Running/undefined not", () => {
    assertEqual(isTerminalPhase("Succeeded"), true, "Succeeded");
    assertEqual(isTerminalPhase("Failed"), true, "Failed");
    assertEqual(isTerminalPhase("Running"), false, "Running");
    assertEqual(isTerminalPhase("Pending"), false, "Pending");
    assertEqual(isTerminalPhase(undefined), false, "undefined");
  });

  console.log("waitForRunning (fake RunApi, no network)");
  const makeApi = (sequence: AgentRun[]): RunApi => {
    let i = 0;
    return {
      listAgents: () => Promise.reject(new Error("unused")),
      listApplications: () => Promise.reject(new Error("unused")),
      listRuns: () => Promise.reject(new Error("unused")),
      createRun: () => Promise.reject(new Error("unused")),
      deleteRun: () => Promise.reject(new Error("unused")),
      getRun: () => Promise.resolve(sequence[Math.min(i++, sequence.length - 1)] as AgentRun),
    };
  };
  const run = (status: AgentRun["status"]): AgentRun => ({
    metadata: { name: "r1" },
    spec: { agentRef: "migration-analyzer" },
    status,
  });

  await check("resolves once Running + sandboxName + secretKeyRef", async () => {
    const phases: string[] = [];
    const api = makeApi([
      run(undefined),
      run({ phase: "Pending" }),
      run({ phase: "Running" }), // Running but not connectable yet
      run({ phase: "Running", sandboxName: "r1", secretKeyRef: { name: "r1-acp-key" } }),
    ]);
    const result = await waitForRunning(api, "r1", {
      pollMs: 5,
      timeoutMs: 2_000,
      onPhase: (p) => phases.push(p),
    });
    assertEqual(result.status?.sandboxName, "r1", "sandboxName");
    assertEqual(phases.join(","), "Pending,Pending,Running,Running", "observed phases");
  });
  await check("rejects on Failed with condition messages", async () => {
    const api = makeApi([
      run({
        phase: "Failed",
        conditions: [
          { type: "Ready", status: "False", message: "image pull failed" },
          { type: "Sandbox", status: "False", message: "sandbox gone" },
        ],
      }),
    ]);
    await assertRejects(waitForRunning(api, "r1", { pollMs: 5, timeoutMs: 2_000 }), [
      "r1 failed",
      "image pull failed",
      "sandbox gone",
    ]);
  });
  await check("rejects on timeout with actionable message", async () => {
    const api = makeApi([run({ phase: "Pending" })]);
    await assertRejects(waitForRunning(api, "r1", { pollMs: 5, timeoutMs: 25 }), [
      "Timed out",
      "phase=Pending",
      "kubectl describe agentrun r1",
    ]);
  });
  await check("rejects promptly when signal aborts", async () => {
    const api = makeApi([run({ phase: "Pending" })]);
    const ac = new AbortController();
    const p = waitForRunning(api, "r1", { pollMs: 5_000, timeoutMs: 60_000, signal: ac.signal });
    setTimeout(() => ac.abort(new Error("user cancelled")), 10);
    await assertRejects(p, ["user cancelled"]);
  });

  console.log(`\n${passed} passed, ${failed} failed`);
  if (failed > 0) {
    throw new Error(`${failed} selfcheck(s) failed`);
  }
}

await main();
