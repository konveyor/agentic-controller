#!/usr/bin/env node
/**
 * Mock of the sandbox harness ACP surface (`goose serve --port 4000`).
 *
 * Speaks real ACP — it is built on @agentclientprotocol/sdk's server side,
 * the same protocol implementation the client uses — but fakes the agent:
 * no LLM calls, deterministic streamed responses. Honors the ADR 0002
 * contract:
 *   - WebSocket + streamable HTTP at :4000/acp
 *   - X-Secret-Key auth from GOOSE_SERVER__SECRET_KEY
 *   - session/new, session/load (full history replay), session/prompt,
 *     session/cancel
 *   - GOOSE_MODE=auto suppresses request_permission
 *
 * Replace with the real harness image when the controller lands; nothing
 * in the client changes.
 *
 * Failure modes for integration tests, triggered by tokens in the prompt:
 *   TEST_PERMISSION — force a session/request_permission round-trip
 *                     (even in GOOSE_MODE=auto) and echo the outcome
 *   TEST_CANCEL     — stream slowly until session/cancel arrives
 *   TEST_DROP       — destroy all TCP connections mid-turn (the pending
 *                     prompt request never gets a response)
 */
import http from "node:http";
import { WebSocketServer } from "ws";
import * as acp from "@agentclientprotocol/sdk";
import { AcpServer } from "@agentclientprotocol/sdk/experimental/server";
import {
  createNodeHttpHandler,
  createNodeWebSocketUpgradeHandler,
} from "@agentclientprotocol/sdk/experimental/node";

const PORT = Number(process.env.PORT ?? 4000);
const SECRET_KEY = process.env.GOOSE_SERVER__SECRET_KEY ?? "";
const GOOSE_MODE = process.env.GOOSE_MODE ?? "auto";
// The real agentic-controller injects KONVEYOR_PROMPT / KONVEYOR_INSTRUCTIONS
// (from Agent.spec.prompt and AgentRun.spec.instructions). Fall back to the
// AGENT_* names for older/standalone invocations.
const AGENT_PROMPT =
  process.env.KONVEYOR_PROMPT ?? process.env.AGENT_PROMPT ?? "(no standing prompt)";
const AGENT_INSTRUCTIONS =
  process.env.KONVEYOR_INSTRUCTIONS ?? process.env.AGENT_INSTRUCTIONS ?? "";

const params = Object.entries(process.env)
  .filter(([k]) => k.startsWith("KONVEYOR_PARAM_"))
  .map(([k, v]) => `${k.slice("KONVEYOR_PARAM_".length).toLowerCase()}=${v}`);

/** sessionId -> { history: SessionNotification[] } (stand-in for goose's SQLite) */
const sessions = new Map();

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const newId = () =>
  Array.from(crypto.getRandomValues(new Uint8Array(12)))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");

async function notifyAndRecord(cx, session, sessionId, update) {
  const notification = { sessionId, update };
  session.history.push(notification);
  await cx.client.notify(acp.methods.client.session.update, notification);
}

function buildAgent() {
  return acp
    .agent({ name: "mock-goose-harness" })
    .onRequest(acp.methods.agent.initialize, () => ({
      protocolVersion: acp.PROTOCOL_VERSION,
      agentCapabilities: { loadSession: true },
    }))
    .onRequest(acp.methods.agent.authenticate, () => ({}))
    .onRequest(acp.methods.agent.session.new, () => {
      const sessionId = newId();
      sessions.set(sessionId, { history: [] });
      console.log(`[mock-harness] session/new -> ${sessionId}`);
      return { sessionId };
    })
    .onRequest(acp.methods.agent.session.load, async (cx) => {
      const session = sessions.get(cx.params.sessionId);
      if (!session) throw new Error(`unknown session ${cx.params.sessionId}`);
      console.log(
        `[mock-harness] session/load ${cx.params.sessionId}: replaying ${session.history.length} updates`,
      );
      for (const notification of session.history) {
        await cx.client.notify(acp.methods.client.session.update, notification);
      }
      return {};
    })
    .onNotification(acp.methods.agent.session.cancel, (cx) => {
      const session = sessions.get(cx.params.sessionId);
      if (session) session.cancelled = true;
      console.log(`[mock-harness] session/cancel ${cx.params.sessionId}`);
    })
    .onRequest(acp.methods.agent.session.prompt, async (cx) => {
      const { sessionId, prompt } = cx.params;
      const session = sessions.get(sessionId);
      if (!session) throw new Error(`unknown session ${sessionId}`);
      session.cancelled = false;

      const userText = prompt
        .map((block) => (block.type === "text" ? block.text : `[${block.type}]`))
        .join(" ");
      console.log(`[mock-harness] session/prompt ${sessionId}: ${userText}`);

      const say = (text) =>
        notifyAndRecord(cx, session, sessionId, {
          sessionUpdate: "agent_message_chunk",
          content: { type: "text", text },
        });

      await say(`Mock harness online. Standing prompt: ${AGENT_PROMPT.trim().slice(0, 80)}\n`);
      await sleep(150);
      if (AGENT_INSTRUCTIONS.trim()) {
        await say(`Run instructions: ${AGENT_INSTRUCTIONS.trim().slice(0, 120)}\n`);
        await sleep(150);
      }
      await say(`Run params: ${params.length ? params.join(", ") : "(none)"}\n`);
      await sleep(150);

      await notifyAndRecord(cx, session, sessionId, {
        sessionUpdate: "tool_call",
        toolCallId: "call_1",
        title: "Scanning workspace",
        kind: "read",
        status: "in_progress",
        locations: [{ path: "/workspace" }],
      });
      await sleep(300);
      await notifyAndRecord(cx, session, sessionId, {
        sessionUpdate: "tool_call_update",
        toolCallId: "call_1",
        status: "completed",
        content: [
          {
            type: "content",
            content: { type: "text", text: "Found 42 source files (mock)." },
          },
        ],
      });

      if (GOOSE_MODE !== "auto" || userText.includes("TEST_PERMISSION")) {
        const response = await cx.client.request(acp.methods.client.session.requestPermission, {
          sessionId,
          toolCall: {
            toolCallId: "call_2",
            title: "Write findings to .konveyor/",
            kind: "edit",
            // Standard ACP ToolCallContent diff blocks — the diff-preview
            // payload UIs render before approving.
            content: [
              {
                type: "diff",
                path: "src/main/java/com/example/InventoryService.java",
                oldText: [
                  "import javax.ejb.Stateless;",
                  "import javax.persistence.EntityManager;",
                  "",
                  "@Stateless",
                  "public class InventoryService {",
                ].join("\n"),
                newText: [
                  "import jakarta.ejb.Stateless;",
                  "import jakarta.persistence.EntityManager;",
                  "",
                  "@Stateless",
                  "public class InventoryService {",
                ].join("\n"),
              },
              {
                type: "diff",
                path: ".konveyor/java-ee-findings.md",
                oldText: null, // new file
                newText: "# Findings\n\n- javax.* imports: 2 (map to jakarta.*)\n",
              },
            ],
          },
          options: [
            { optionId: "allow", name: "Allow", kind: "allow_once" },
            { optionId: "reject", name: "Reject", kind: "reject_once" },
          ],
        });
        await say(`Permission outcome: ${JSON.stringify(response.outcome)}\n`);
      }

      if (userText.includes("TEST_CANCEL")) {
        for (let i = 0; i < 50 && !session.cancelled; i++) {
          await say(`still working (${i})...\n`);
          await sleep(200);
        }
      }

      if (userText.includes("TEST_DROP")) {
        await say("about to lose connectivity\n");
        setTimeout(() => {
          console.log(`[mock-harness] TEST_DROP: destroying ${rawSockets.size} connection(s)`);
          for (const s of rawSockets) s.destroy();
        }, 100);
        await sleep(10_000); // response never reaches the client
      }

      if (session.cancelled) return { stopReason: "cancelled" };

      await say(`Echo of your instructions: "${userText}"\n`);
      await say("Turn complete.\n");
      return { stopReason: "end_turn" };
    });
}

const acpServer = new AcpServer({ createAgent: buildAgent });
const httpHandler = createNodeHttpHandler(acpServer);
const wss = new WebSocketServer({ noServer: true });
const upgradeHandler = createNodeWebSocketUpgradeHandler(acpServer, wss);

function authorized(req) {
  if (!SECRET_KEY) return true; // no key configured = open (dev only)
  return req.headers["x-secret-key"] === SECRET_KEY;
}

const server = http.createServer((req, res) => {
  if (req.url === "/healthz") {
    res.writeHead(200).end("ok");
    return;
  }
  if (!authorized(req)) {
    res.writeHead(401, { "content-type": "text/plain" }).end("invalid X-Secret-Key");
    return;
  }
  httpHandler(req, res);
});

const rawSockets = new Set();
server.on("connection", (socket) => {
  rawSockets.add(socket);
  socket.on("close", () => rawSockets.delete(socket));
});

server.on("upgrade", (req, socket, head) => {
  if (!authorized(req)) {
    socket.write("HTTP/1.1 401 Unauthorized\r\nConnection: close\r\n\r\n");
    socket.destroy();
    return;
  }
  upgradeHandler(req, socket, head);
});

server.listen(PORT, () => {
  console.log(
    `[mock-harness] ACP listening on :${PORT}/acp (auth=${SECRET_KEY ? "X-Secret-Key" : "OPEN"}, GOOSE_MODE=${GOOSE_MODE})`,
  );
});
