/**
 * Deterministic check of the readiness-aware ACP dial (src/acp-dial.ts).
 *
 * Reproduces the exact failure the fix targets — a pod that accepts the ACP
 * connection only AFTER the initial dial — using a local WebSocket server that
 * binds late. Also asserts the two boundaries: fail-fast on a non-readiness
 * rejection (401), and the control that a single un-retried dial (the old
 * behavior) fails immediately.
 *
 * Run:  npx tsx dev/dial-check.ts     (no cluster needed)
 */
import { createServer } from "node:http";
import { AddressInfo } from "node:net";
import { WebSocket as WsWebSocket, WebSocketServer } from "ws";
import { connectUpstream, isNotReadyDialError } from "../src/acp-dial.js";

let failures = 0;
const pass = (s: string, d?: string) => console.log(`PASS ${s}${d ? ` — ${d}` : ""}`);
const fail = (s: string, d: string) => {
  failures++;
  console.error(`FAIL ${s} — ${d}`);
};
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Reserve a free localhost port, then release it. */
async function freePort(): Promise<number> {
  const srv = createServer();
  await new Promise<void>((resolve) => srv.listen(0, "127.0.0.1", resolve));
  const port = (srv.address() as AddressInfo).port;
  await new Promise<void>((resolve) => srv.close(() => resolve()));
  return port;
}

async function main(): Promise<void> {
  // 1) Late-bind: nothing is listening when the dial starts; the ACP server
  //    comes up ~1.2s later. The fix must retry through the ECONNREFUSED
  //    window and open once the server binds.
  {
    const port = await freePort();
    const target = `ws://127.0.0.1:${port}/acp`;
    let wss: WebSocketServer | undefined;
    const bindDelayMs = 1_200;
    setTimeout(() => {
      wss = new WebSocketServer({ port, host: "127.0.0.1" });
      wss.on("connection", (ws) => ws.send("hello"));
    }, bindDelayMs);

    const started = Date.now();
    const logs: string[] = [];
    try {
      const ws = await connectUpstream(target, {
        secretKey: "k",
        tag: "acp check",
        isClientClosed: () => false,
        timeoutMs: 20_000,
        retryMs: 200,
        log: (m) => logs.push(m),
      });
      const elapsed = Date.now() - started;
      if (ws.readyState !== WsWebSocket.OPEN) fail("late-bind", `socket not OPEN (state=${ws.readyState})`);
      else if (elapsed < bindDelayMs) fail("late-bind", `opened too early (${elapsed}ms < ${bindDelayMs}ms bind delay)`);
      else if (!logs.some((l) => l.includes("retrying dial"))) fail("late-bind", `no retry was logged: ${JSON.stringify(logs)}`);
      else pass("late-bind", `connected after ${elapsed}ms; logs=${JSON.stringify(logs)}`);
      ws.terminate();
    } catch (err) {
      fail("late-bind", `threw instead of connecting: ${(err as Error).message}`);
    } finally {
      wss?.close();
    }
  }

  // 2) Fail-fast: a 401 handshake rejection is NOT a readiness signal, so the
  //    dial must surface it immediately, not burn the whole budget retrying.
  {
    const srv = createServer();
    srv.on("upgrade", (_req, socket) => {
      socket.end("HTTP/1.1 401 Unauthorized\r\nConnection: close\r\n\r\n");
    });
    await new Promise<void>((resolve) => srv.listen(0, "127.0.0.1", resolve));
    const port = (srv.address() as AddressInfo).port;
    const target = `ws://127.0.0.1:${port}/acp`;

    const started = Date.now();
    try {
      const ws = await connectUpstream(target, {
        secretKey: "k",
        tag: "acp check",
        isClientClosed: () => false,
        timeoutMs: 20_000,
        retryMs: 200,
      });
      ws.terminate();
      fail("fail-fast", "unexpectedly connected to a 401 endpoint");
    } catch (err) {
      const elapsed = Date.now() - started;
      if (elapsed > 3_000) fail("fail-fast", `took ${elapsed}ms — looks like it retried a non-readiness error`);
      else pass("fail-fast", `rejected in ${elapsed}ms: ${(err as Error).message}`);
    } finally {
      srv.close();
    }
  }

  // 3) Control: a single, un-retried dial to a not-yet-listening port fails
  //    immediately — the pre-fix behavior that surfaced to the browser as 1011.
  {
    const port = await freePort();
    const target = `ws://127.0.0.1:${port}/acp`;
    const err = await new Promise<Error | undefined>((resolve) => {
      const ws = new WsWebSocket(target);
      ws.once("open", () => {
        ws.terminate();
        resolve(undefined);
      });
      ws.once("error", (e: Error) => resolve(e));
    });
    if (!err) fail("control", "single dial unexpectedly opened");
    else if (!isNotReadyDialError(err)) fail("control", `error not classified as not-ready: ${err.message}`);
    else pass("control", `single dial failed as expected (${err.message}) — this is what used to become 1011`);
  }

  await sleep(50);
  console.log(failures === 0 ? "DIAL-CHECK PASS" : `DIAL-CHECK FAIL (${failures})`);
  process.exitCode = failures === 0 ? 0 : 1;
}

void main();
