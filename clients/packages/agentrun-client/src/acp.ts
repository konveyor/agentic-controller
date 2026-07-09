/**
 * ACP-side client: connects to a run's `goose serve` endpoint
 * (ws://<host>:4000/acp, authenticated with X-Secret-Key per ADR 0002)
 * using the official @agentclientprotocol/sdk WebSocket transport.
 *
 * In a VSCode extension host this runs under Node, so we pass the `ws`
 * constructor explicitly — browser WebSocket cannot set upgrade headers.
 */
import { WebSocket } from "ws";
import * as acp from "@agentclientprotocol/sdk";
import { createWebSocketStream } from "@agentclientprotocol/sdk/experimental/ws-client";
import type { AcpEndpoint } from "./kube.js";

export const ACP_PATH = "/acp";
export const SECRET_KEY_HEADER = "X-Secret-Key";

export interface AcpTarget {
  host: string;
  port: number;
  secretKey: string;
}

export function targetFromEndpoint(endpoint: AcpEndpoint, localPort?: number): AcpTarget {
  return localPort !== undefined
    ? { host: "127.0.0.1", port: localPort, secretKey: endpoint.secretKey }
    : { host: endpoint.serviceHost, port: endpoint.port, secretKey: endpoint.secretKey };
}

export interface RunClientHandlers {
  /** Streaming session updates (message chunks, tool calls, plans...). */
  onSessionUpdate?: (notification: acp.SessionNotification) => void | Promise<void>;
  /**
   * Human-in-the-loop approval. In the extension this maps onto the diff
   * preview UX; the default rejects nothing and picks the first option.
   */
  onPermissionRequest?: (
    request: acp.RequestPermissionRequest,
  ) => acp.RequestPermissionResponse | Promise<acp.RequestPermissionResponse>;
  clientName?: string;
}

export interface RunConnection {
  initialized: acp.InitializeResponse;
  ctx: acp.ClientContext;
  /** Starts a fresh session in the sandbox workspace. */
  newSession(cwd?: string): Promise<acp.NewSessionResponse>;
  /** Replays full history for an existing session, then streams live. */
  loadSession(sessionId: string, cwd?: string): Promise<acp.LoadSessionResponse>;
  /** Sends a text prompt and resolves when the turn completes. */
  prompt(sessionId: string, text: string): Promise<acp.PromptResponse>;
  cancel(sessionId: string): Promise<void>;
}

/**
 * Opens an authenticated ACP connection and runs `op` over it. The
 * connection is closed when `op` settles.
 */
export async function withRunConnection<T>(
  target: AcpTarget,
  handlers: RunClientHandlers,
  op: (conn: RunConnection) => Promise<T>,
): Promise<T> {
  const stream = createWebSocketStream(`ws://${target.host}:${target.port}${ACP_PATH}`, {
    WebSocket,
    headers: { [SECRET_KEY_HEADER]: target.secretKey },
  });

  const app = acp
    .client({ name: handlers.clientName ?? "konveyor-agentrun-client" })
    .onNotification(acp.methods.client.session.update, (c) =>
      handlers.onSessionUpdate?.(c.params),
    )
    .onRequest(acp.methods.client.session.requestPermission, async (c) => {
      if (handlers.onPermissionRequest) return handlers.onPermissionRequest(c.params);
      const first = c.params.options[0];
      return {
        outcome: first
          ? { outcome: "selected", optionId: first.optionId }
          : { outcome: "cancelled" },
      };
    });

  try {
    return await app.connectWith(stream, async (ctx) => {
      const initialized = await ctx.request(acp.methods.agent.initialize, {
        protocolVersion: acp.PROTOCOL_VERSION,
        clientCapabilities: {},
      });

      const conn: RunConnection = {
        initialized,
        ctx,
        newSession: (cwd = "/workspace") =>
          ctx.request(acp.methods.agent.session.new, { cwd, mcpServers: [] }),
        loadSession: (sessionId, cwd = "/workspace") =>
          ctx.request(acp.methods.agent.session.load, { sessionId, cwd, mcpServers: [] }),
        prompt: (sessionId, text) =>
          ctx.request(acp.methods.agent.session.prompt, {
            sessionId,
            prompt: [{ type: "text", text }],
          }),
        cancel: (sessionId) =>
          ctx.notify(acp.methods.agent.session.cancel, { sessionId }),
      };
      return op(conn);
    });
  } finally {
    await stream.writable.close().catch(() => {});
  }
}
