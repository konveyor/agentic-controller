/**
 * Verifies that a dead agent-side connection SURFACES to the browser client
 * instead of hanging the session forever — the TEST_DROP scenario.
 *
 * Two checks:
 *   A) direct-dial (no cluster): the upstream WebSocket itself must see a
 *      close/error when the server destroys the TCP connection mid-stream.
 *      This is the ACP_DIAL=direct path — no tunnel in between.
 *   B) tunnel E2E (live shim + cluster, like browser-smoke): create a mock
 *      run, prompt it with TEST_DROP (the mock harness destroys all TCP
 *      connections mid-turn), and require the browser-side WebSocket to be
 *      closed with 1011 within a budget. Pre-fix behavior: the port-forward
 *      tunnel swallowed the pod-side teardown and this hung indefinitely.
 *
 * Run:  npm run drop-check          (SHIM_URL overrides base)
 *       DROP_CHECK_LOCAL_ONLY=1 npm run drop-check   (section A only)
 */
import type { Socket } from "node:net";
import { WebSocketServer } from "ws";
import { connectUpstream } from "../src/acp-dial.js";

const BASE = process.env.SHIM_URL ?? "http://127.0.0.1:7080";
const WS_BASE = BASE.replace(/^http/, "ws");
const RUNNING_TIMEOUT_MS = 120_000;
/**
 * Ceiling for the drop to surface. The tunnel teardown fix propagates in
 * ~milliseconds; the keepalive backstop needs up to 2× ACP_KEEPALIVE_MS
 * (default 10s). Anything past this means the hang is back.
 */
const DROP_CLOSE_BUDGET_MS = Number(process.env.DROP_CLOSE_BUDGET_MS ?? 25_000);

let failures = 0;
const pass = (s: string, d?: string) => console.log(`PASS ${s}${d ? ` — ${d}` : ""}`);
const fail = (s: string, d: string) => {
  failures++;
  console.error(`FAIL ${s} — ${d}`);
};
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// ------------------------------------------------- A) direct-dial teardown

async function checkDirectDial(): Promise<void> {
  const rawSockets = new Set<Socket>();
  const wss = new WebSocketServer({ port: 0, host: "127.0.0.1" });
  await new Promise<void>((resolve) => wss.once("listening", resolve));
  const port = (wss.address() as { port: number }).port;
  wss.on("connection", (ws, req) => {
    rawSockets.add(req.socket);
    ws.send("hello");
  });

  try {
    const ws = await connectUpstream(`ws://127.0.0.1:${port}/acp`, {
      secretKey: "k",
      tag: "drop check",
      isClientClosed: () => false,
      timeoutMs: 5_000,
      retryMs: 100,
    });
    const closed = new Promise<string>((resolve) => {
      ws.on("close", (code: number) => resolve(`close code=${code}`));
      ws.on("error", (err: Error) => resolve(`error ${err.message}`));
    });
    await sleep(100); // let the hello frame land, mirror an established session
    for (const s of rawSockets) s.destroy(); // what TEST_DROP does pod-side
    const started = Date.now();
    const outcome = await Promise.race([closed, sleep(3_000).then(() => undefined)]);
    if (!outcome) {
      fail("direct-dial", "destroyed server socket never surfaced on the client (3s)");
      ws.terminate();
    } else {
      pass("direct-dial", `surfaced as ${outcome} after ${Date.now() - started}ms`);
    }
  } catch (err) {
    fail("direct-dial", `dial failed: ${(err as Error).message}`);
  } finally {
    wss.close();
  }
}

// ------------------------------------------------- B) tunnel E2E via shim

async function fetchJson(
  path: string,
  init?: RequestInit,
): Promise<{ status: number; body: unknown }> {
  const res = await fetch(`${BASE}${path}`, init);
  const text = await res.text();
  let body: unknown;
  try {
    body = text ? JSON.parse(text) : undefined;
  } catch {
    body = text;
  }
  return { status: res.status, body };
}

type RunView = { metadata?: { name?: string }; status?: { phase?: string } };

/** Send one JSON-RPC request and await its response frame. */
function rpc(ws: WebSocket, id: number, method: string, params: unknown): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${method} timed out (30s)`)), 30_000);
    const onMessage = (ev: MessageEvent) => {
      void (async () => {
        const text = typeof ev.data === "string" ? ev.data : await (ev.data as Blob).text();
        const msg = JSON.parse(text) as { id?: number; result?: unknown; error?: { message: string } };
        if (msg.id !== id) return;
        ws.removeEventListener("message", onMessage);
        clearTimeout(timer);
        if (msg.error) reject(new Error(`${method}: ${msg.error.message}`));
        else resolve(msg.result);
      })();
    };
    ws.addEventListener("message", onMessage);
    ws.send(JSON.stringify({ jsonrpc: "2.0", id, method, params }));
  });
}

async function checkTunnelDrop(): Promise<void> {
  const health = await fetch(`${BASE}/healthz`).catch(() => undefined);
  if (!health || health.status !== 200) {
    fail("tunnel-drop", `shim not reachable at ${BASE} — start it (hack/demo-up.sh) or set DROP_CHECK_LOCAL_ONLY=1`);
    return;
  }

  let runName: string | undefined;
  try {
    const { status, body } = await fetchJson("/api/agentruns", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        agentRef: "migration-analyzer",
        params: {
          repository: "https://github.com/konveyor-ecosystem/coolstore",
          branch: "main",
        },
        instructions: "drop-check",
      }),
    });
    const created = body as RunView;
    if (status !== 201 || !created?.metadata?.name) {
      fail("tunnel-drop", `create run: status=${status} body=${JSON.stringify(body)}`);
      return;
    }
    runName = created.metadata.name;

    const deadline = Date.now() + RUNNING_TIMEOUT_MS;
    for (;;) {
      const run = (await fetchJson(`/api/agentruns/${runName}`)).body as RunView;
      const phase = run?.status?.phase ?? "unset";
      if (phase === "Running") break;
      if (phase === "Failed" || Date.now() > deadline) {
        fail("tunnel-drop", `run never Running (phase=${phase})`);
        return;
      }
      await sleep(2_000);
    }

    const ws = new WebSocket(`${WS_BASE}/api/agentruns/${runName}/acp`);
    await new Promise<void>((resolve, reject) => {
      ws.addEventListener("open", () => resolve(), { once: true });
      ws.addEventListener("error", () => reject(new Error("ws failed to open")), { once: true });
    });
    const closed = new Promise<CloseEvent>((resolve) => {
      ws.addEventListener("close", resolve, { once: true });
    });

    await rpc(ws, 1, "initialize", { protocolVersion: 1, clientCapabilities: {} });
    const session = (await rpc(ws, 2, "session/new", { cwd: "/workspace", mcpServers: [] })) as {
      sessionId?: string;
    };
    if (!session?.sessionId) {
      fail("tunnel-drop", `session/new returned ${JSON.stringify(session)}`);
      return;
    }

    // The response to this prompt never arrives — the harness kills every
    // TCP connection mid-turn. What MUST arrive is the close event.
    ws.send(
      JSON.stringify({
        jsonrpc: "2.0",
        id: 3,
        method: "session/prompt",
        params: {
          sessionId: session.sessionId,
          prompt: [{ type: "text", text: "connectivity drill: TEST_DROP" }],
        },
      }),
    );
    const started = Date.now();
    const outcome = await Promise.race([
      closed,
      sleep(DROP_CLOSE_BUDGET_MS).then(() => undefined),
    ]);
    if (!outcome) {
      fail(
        "tunnel-drop",
        `browser socket still open ${DROP_CLOSE_BUDGET_MS}ms after TEST_DROP — the pre-fix hang`,
      );
      ws.close();
    } else if (outcome.code !== 1011) {
      fail("tunnel-drop", `closed with code=${outcome.code} (expected 1011) reason=${outcome.reason}`);
    } else {
      pass(
        "tunnel-drop",
        `client closed code=1011 reason="${outcome.reason}" ${Date.now() - started}ms after prompt`,
      );
    }
  } finally {
    if (runName) {
      await fetchJson(`/api/agentruns/${runName}`, { method: "DELETE" }).catch(() => undefined);
    }
  }
}

async function main(): Promise<void> {
  await checkDirectDial();
  if (process.env.DROP_CHECK_LOCAL_ONLY !== "1") await checkTunnelDrop();
  console.log(failures === 0 ? "DROP-CHECK PASS" : `DROP-CHECK FAIL (${failures})`);
  process.exitCode = failures === 0 ? 0 : 1;
}

void main();
