/**
 * Browser-emulating smoke test for the hub-shim.
 *
 * Uses ONLY globalThis.fetch and globalThis.WebSocket — no 'ws' import, no
 * custom headers — exactly what a browser UI can do. The ACP JSON-RPC 2.0
 * framing is spoken directly over the WebSocket (method names mirror
 * @agentclientprotocol/sdk acp.methods: initialize, session/new,
 * session/load, session/prompt; notifications session/update; server→client
 * request session/request_permission).
 *
 * Run with the shim already up:  npm run smoke   (SHIM_URL overrides base)
 * Exits non-zero if any step fails. Creates one AgentRun and deletes it.
 */

const BASE = process.env.SHIM_URL ?? "http://127.0.0.1:7080";
const WS_BASE = BASE.replace(/^http/, "ws");
const RUNNING_TIMEOUT_MS = 120_000;
const RPC_TIMEOUT_MS = 60_000;

let failures = 0;
function pass(step: string, detail?: string): void {
  console.log(`PASS ${step}${detail ? ` — ${detail}` : ""}`);
}
function fail(step: string, detail: string): never {
  failures++;
  console.error(`FAIL ${step} — ${detail}`);
  throw new SmokeAbort(step);
}
class SmokeAbort extends Error {
  constructor(step: string) {
    super(`aborted at step: ${step}`);
  }
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// ------------------------------------------------------------ tiny ACP rpc

interface RpcResponse {
  jsonrpc: "2.0";
  id?: number | string;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

type SessionUpdateParams = {
  sessionId: string;
  update: { sessionUpdate: string; [k: string]: unknown };
};

async function frameToText(data: unknown): Promise<string> {
  if (typeof data === "string") return data;
  if (data instanceof Blob) return data.text();
  if (data instanceof ArrayBuffer) return new TextDecoder().decode(data);
  throw new Error(`unexpected WebSocket frame type: ${Object.prototype.toString.call(data)}`);
}

/** Minimal browser-side ACP peer: requests out, notifications/requests in. */
class BrowserAcpSocket {
  readonly updates: SessionUpdateParams[] = [];
  private nextId = 1;
  private readonly pending = new Map<
    number,
    { resolve: (v: unknown) => void; reject: (e: Error) => void }
  >();

  private constructor(private readonly ws: WebSocket) {}

  static open(url: string): Promise<BrowserAcpSocket> {
    return new Promise((resolve, reject) => {
      // Browser constraint: no headers argument exists — the shim injects
      // X-Secret-Key on the upstream leg.
      const ws = new WebSocket(url);
      const sock = new BrowserAcpSocket(ws);
      ws.addEventListener("open", () => resolve(sock), { once: true });
      ws.addEventListener("close", (ev: CloseEvent) => {
        reject(new Error(`socket closed before open (code=${ev.code} reason=${ev.reason})`));
        sock.failAllPending(new Error(`socket closed (code=${ev.code} reason=${ev.reason})`));
      });
      ws.addEventListener("error", () => {
        reject(new Error("socket error before open"));
      });
      ws.addEventListener("message", (ev: MessageEvent) => {
        void frameToText(ev.data)
          .then((text) => sock.handleFrame(text))
          .catch((err: unknown) => console.error("bad frame:", err));
      });
    });
  }

  private failAllPending(err: Error): void {
    for (const [, p] of this.pending) p.reject(err);
    this.pending.clear();
  }

  private handleFrame(text: string): void {
    const msg = JSON.parse(text) as RpcResponse;
    if (msg.id !== undefined && msg.method === undefined) {
      // Response to one of our requests.
      const entry = this.pending.get(msg.id as number);
      if (!entry) return;
      this.pending.delete(msg.id as number);
      if (msg.error) entry.reject(new Error(`${msg.method ?? "rpc"} error ${msg.error.code}: ${msg.error.message}`));
      else entry.resolve(msg.result);
      return;
    }
    if (msg.method === "session/update") {
      this.updates.push(msg.params as SessionUpdateParams);
      return;
    }
    if (msg.id !== undefined && msg.method === "session/request_permission") {
      // Auto-approve: pick the first offered option.
      const params = msg.params as {
        options?: { optionId: string; name: string; kind: string }[];
      };
      const optionId = params.options?.[0]?.optionId;
      this.ws.send(
        JSON.stringify({
          jsonrpc: "2.0",
          id: msg.id,
          result: { outcome: { outcome: "selected", optionId } },
        }),
      );
      return;
    }
    if (msg.id !== undefined && msg.method !== undefined) {
      this.ws.send(
        JSON.stringify({
          jsonrpc: "2.0",
          id: msg.id,
          error: { code: -32601, message: `unsupported method ${msg.method}` },
        }),
      );
    }
    // Other notifications: ignore.
  }

  request(method: string, params: unknown, timeoutMs = RPC_TIMEOUT_MS): Promise<unknown> {
    const id = this.nextId++;
    return new Promise<unknown>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`${method} timed out after ${timeoutMs}ms`));
      }, timeoutMs);
      this.pending.set(id, {
        resolve: (v) => {
          clearTimeout(timer);
          resolve(v);
        },
        reject: (e) => {
          clearTimeout(timer);
          reject(e);
        },
      });
      this.ws.send(JSON.stringify({ jsonrpc: "2.0", id, method, params }));
    });
  }

  close(): Promise<void> {
    return new Promise((resolve) => {
      if (this.ws.readyState === WebSocket.CLOSED) return resolve();
      this.ws.addEventListener("close", () => resolve(), { once: true });
      this.ws.close(1000, "smoke done");
    });
  }
}

// ----------------------------------------------------------------- helpers

async function fetchJson(path: string, init?: RequestInit): Promise<{ status: number; body: unknown }> {
  const res = await fetch(`${BASE}${path}`, init);
  const text = await res.text();
  let body: unknown = undefined;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = text;
    }
  }
  return { status: res.status, body };
}

type RunView = {
  metadata?: { name?: string };
  status?: { phase?: string; conditions?: { message?: string }[] };
};

// -------------------------------------------------------------------- main

async function main(): Promise<void> {
  let runName: string | undefined;
  try {
    // healthz
    {
      const res = await fetch(`${BASE}/healthz`);
      const text = await res.text();
      if (res.status !== 200 || text !== "ok") fail("healthz", `status=${res.status} body=${text}`);
      pass("healthz");
    }

    // list agents
    {
      const { status, body } = await fetchJson("/api/agents");
      if (status !== 200 || !Array.isArray(body)) fail("list-agents", `status=${status}`);
      const names = (body as { metadata?: { name?: string } }[]).map((a) => a.metadata?.name);
      if (!names.includes("migration-analyzer")) {
        fail("list-agents", `migration-analyzer not in [${names.join(", ")}]`);
      }
      pass("list-agents", `agents: ${names.join(", ")}`);
    }

    // create run
    {
      const { status, body } = await fetchJson("/api/agentruns", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          agentRef: "migration-analyzer",
          params: {
            repository: "https://github.com/konveyor-ecosystem/coolstore",
            branch: "main",
          },
          instructions: "UI smoke",
        }),
      });
      const created = body as RunView;
      if (status !== 201 || !created?.metadata?.name) {
        fail("create-run", `status=${status} body=${JSON.stringify(body)}`);
      }
      runName = created.metadata!.name!;
      pass("create-run", runName);
    }

    // poll until Running
    {
      const deadline = Date.now() + RUNNING_TIMEOUT_MS;
      let phase = "unset";
      for (;;) {
        const { status, body } = await fetchJson(`/api/agentruns/${runName}`);
        if (status !== 200) fail("wait-running", `GET run status=${status}`);
        const run = body as RunView;
        phase = run.status?.phase ?? "unset";
        if (phase === "Running") break;
        if (phase === "Failed") {
          const msgs = (run.status?.conditions ?? []).map((c) => c.message).filter(Boolean);
          fail("wait-running", `run Failed: ${msgs.join("; ")}`);
        }
        if (Date.now() > deadline) {
          fail("wait-running", `timed out after ${RUNNING_TIMEOUT_MS}ms (phase=${phase})`);
        }
        await sleep(2_000);
      }
      pass("wait-running", `phase=${phase}`);
    }

    const acpUrl = `${WS_BASE}/api/agentruns/${runName}/acp`;

    // connect + initialize + session/new + prompt
    let sessionId: string;
    let liveUpdateCount: number;
    {
      const sock = await BrowserAcpSocket.open(acpUrl);
      pass("ws-connect", acpUrl);

      const init = (await sock.request("initialize", {
        protocolVersion: 1,
        clientCapabilities: {},
      })) as { protocolVersion?: number; agentCapabilities?: { loadSession?: boolean } };
      if (typeof init?.protocolVersion !== "number") {
        fail("initialize", `unexpected response: ${JSON.stringify(init)}`);
      }
      pass(
        "initialize",
        `protocolVersion=${init.protocolVersion} loadSession=${init.agentCapabilities?.loadSession ?? false}`,
      );

      const created = (await sock.request("session/new", {
        cwd: "/workspace",
        mcpServers: [],
      })) as { sessionId?: string };
      if (!created?.sessionId) fail("session-new", `no sessionId: ${JSON.stringify(created)}`);
      sessionId = created.sessionId!;
      pass("session-new", sessionId);

      const result = (await sock.request(
        "session/prompt",
        {
          sessionId,
          prompt: [{ type: "text", text: "Start the migration analysis." }],
        },
        RPC_TIMEOUT_MS,
      )) as { stopReason?: string };
      if (result?.stopReason !== "end_turn") {
        fail("prompt", `stopReason=${result?.stopReason} (expected end_turn)`);
      }
      liveUpdateCount = sock.updates.length;
      const chunkWithPrompt = sock.updates.find(
        (u) =>
          u.update?.sessionUpdate === "agent_message_chunk" &&
          JSON.stringify(u.update).includes("Standing prompt"),
      );
      if (!chunkWithPrompt) {
        fail(
          "prompt",
          `no agent_message_chunk containing "Standing prompt" in ${liveUpdateCount} updates`,
        );
      }
      pass("prompt", `stopReason=end_turn, ${liveUpdateCount} updates streamed`);
      await sock.close();
    }

    // fresh connection + session/load replay
    {
      const sock = await BrowserAcpSocket.open(acpUrl);
      await sock.request("initialize", { protocolVersion: 1, clientCapabilities: {} });
      await sock.request("session/load", { sessionId, cwd: "/workspace", mcpServers: [] });
      // Replay notifications are sent before the load response resolves, but
      // give the event loop a beat in case any frame is still in flight.
      await sleep(250);
      const replayed = sock.updates.length;
      if (replayed === 0) fail("session-load", "no updates replayed");
      pass("session-load", `${replayed} updates replayed (live turn streamed ${liveUpdateCount})`);
      await sock.close();
    }

    // delete run
    {
      const { status } = await fetchJson(`/api/agentruns/${runName}`, { method: "DELETE" });
      if (status !== 204) fail("delete-run", `status=${status}`);
      pass("delete-run", runName);
      runName = undefined;
    }
  } catch (err) {
    if (!(err instanceof SmokeAbort)) {
      failures++;
      console.error(`FAIL unexpected — ${err instanceof Error ? err.message : String(err)}`);
    }
  } finally {
    // Best-effort cleanup if we bailed after creating the run.
    if (runName) {
      await fetchJson(`/api/agentruns/${runName}`, { method: "DELETE" }).catch(() => undefined);
      console.log(`(cleanup) deleted ${runName}`);
    }
  }

  console.log(failures === 0 ? "SMOKE PASS" : `SMOKE FAIL (${failures} failure(s))`);
  process.exitCode = failures === 0 ? 0 : 1;
}

void main();
