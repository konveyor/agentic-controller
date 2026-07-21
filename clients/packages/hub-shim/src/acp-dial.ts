/**
 * Readiness-aware ACP dial.
 *
 * waitForAcpEndpoint gates on the AgentRun's phase=Running, but a sandbox pod
 * can be Running — and, under the real agentic-controller, Ready, since that
 * controller declares no readiness probe on the harness container — before the
 * harness process has bound :4000. Dialing into that window resets the socket,
 * which Node surfaces as "socket hang up". Treating the ACP dial itself as the
 * readiness signal (retry until it opens) closes the race regardless of which
 * controller provisioned the pod, mirroring the simulator pod's own
 * tcpSocket:4000 probe.
 */
import { WebSocket as WsWebSocket } from "ws";

export const ACP_READY_TIMEOUT_MS = Number(process.env.ACP_READY_TIMEOUT_MS ?? 45_000);
export const ACP_READY_RETRY_MS = Number(process.env.ACP_READY_RETRY_MS ?? 300);
/**
 * Per-attempt open cap. A dial whose port-forward established its stream to a
 * pod that is not yet listening on :4000 can hang without ever firing
 * open/error/close; since the readiness deadline is only enforced BETWEEN
 * attempts, one such stall would block the whole budget. Abort the attempt
 * after this long and retry (a healthy ACP dial opens in well under a second).
 */
export const ACP_DIAL_ATTEMPT_MS = Number(process.env.ACP_DIAL_ATTEMPT_MS ?? 3_000);

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/**
 * A dial failure meaning "the ACP server isn't listening yet" (retry) rather
 * than a real rejection like an auth 401 (surface immediately). Until the
 * harness binds :4000 the tunnel/direct dial refuses or resets mid-handshake:
 * ECONNREFUSED, ECONNRESET, or Node's "socket hang up".
 */
export function isNotReadyDialError(err: unknown): boolean {
  const code = (err as { code?: string })?.code;
  if (code === "ECONNREFUSED" || code === "ECONNRESET" || code === "EPIPE") return true;
  const message = err instanceof Error ? err.message : String(err);
  return /socket hang up|ECONNREFUSED|ECONNRESET|EPIPE|before open/i.test(message);
}

export interface ConnectUpstreamOptions {
  /** Injected as the X-Secret-Key header the browser cannot set. */
  secretKey: string;
  /** Log prefix identifying the run, e.g. "acp ui-abc". */
  tag: string;
  /** Abandon the dial if the browser client has gone away. */
  isClientClosed: () => boolean;
  timeoutMs?: number;
  retryMs?: number;
  log?: (msg: string) => void;
}

/**
 * Opens the upstream ACP WebSocket, retrying while the dial fails with a
 * not-ready signal until it opens or the readiness budget expires. Resolves
 * with an OPEN socket (no listeners attached). Throws the last dial error if
 * the endpoint never comes up, or immediately if the failure is not
 * readiness-related (e.g. a 401 handshake rejection — retrying is pointless).
 */
export async function connectUpstream(
  target: string,
  opts: ConnectUpstreamOptions,
): Promise<WsWebSocket> {
  const { secretKey, tag, isClientClosed } = opts;
  const timeoutMs = opts.timeoutMs ?? ACP_READY_TIMEOUT_MS;
  const retryMs = opts.retryMs ?? ACP_READY_RETRY_MS;
  const log = opts.log ?? (() => {});
  const deadline = Date.now() + timeoutMs;
  let attempt = 0;
  for (;;) {
    if (isClientClosed()) throw new Error("browser client closed before ACP endpoint was ready");
    attempt++;
    const ws = new WsWebSocket(target, { headers: { "X-Secret-Key": secretKey } });
    try {
      await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(
          () => reject(new Error("dial attempt stalled before open")),
          ACP_DIAL_ATTEMPT_MS,
        );
        ws.once("open", () => {
          clearTimeout(timer);
          resolve();
        });
        ws.once("error", (err) => {
          clearTimeout(timer);
          reject(err);
        });
        ws.once("close", () => {
          clearTimeout(timer);
          reject(new Error("upstream closed before open"));
        });
      });
      if (attempt > 1) log(`${tag} upstream ready after ${attempt} attempts`);
      return ws;
    } catch (err) {
      ws.removeAllListeners();
      // terminate() on a still-CONNECTING socket (the per-attempt stall path)
      // emits 'error' asynchronously; without a listener that is an unhandled
      // 'error' event that crashes the process. Absorb it.
      ws.on("error", () => {});
      ws.terminate();
      if (!isNotReadyDialError(err) || Date.now() + retryMs >= deadline) throw err;
      if (attempt === 1) log(`${tag} ACP not listening yet, retrying dial…`);
      await sleep(retryMs);
    }
  }
}
