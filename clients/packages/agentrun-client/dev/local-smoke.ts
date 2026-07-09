/**
 * Local smoke test: exercises src/acp.ts against a mock harness running on
 * localhost (no cluster involved). Covers WS auth, streaming updates,
 * prompt turn completion, and session/load replay.
 *
 *   GOOSE_SERVER__SECRET_KEY=localtest PORT=4100 node ../../harness-mock/server.mjs &
 *   npx tsx dev/local-smoke.ts
 */
import { withRunConnection } from "../src/acp.js";

const target = {
  host: "127.0.0.1",
  port: Number(process.env.PORT ?? 4100),
  secretKey: process.env.GOOSE_SERVER__SECRET_KEY ?? "localtest",
};

let updates = 0;
let text = "";
let sessionId = "";

await withRunConnection(
  target,
  {
    onSessionUpdate: (n) => {
      updates++;
      const u = n.update;
      if (u.sessionUpdate === "agent_message_chunk" && u.content.type === "text") {
        text += u.content.text;
      }
    },
  },
  async (conn) => {
    if (conn.initialized.agentCapabilities?.loadSession !== true) {
      throw new Error("expected loadSession capability");
    }
    const session = await conn.newSession();
    sessionId = session.sessionId;
    const result = await conn.prompt(sessionId, "hello from local smoke test");
    if (result.stopReason !== "end_turn") throw new Error(`unexpected stop: ${result.stopReason}`);
  },
);

if (updates < 4) throw new Error(`expected streamed updates, got ${updates}`);
if (!text.includes("hello from local smoke test")) throw new Error("echo missing from stream");

// Reconnect fresh and replay history.
let replayed = 0;
await withRunConnection(
  target,
  { onSessionUpdate: () => void replayed++ },
  async (conn) => {
    await conn.loadSession(sessionId);
  },
);
if (replayed !== updates) {
  throw new Error(`replay mismatch: live=${updates} replayed=${replayed}`);
}

console.log(`OK: ${updates} live updates, ${replayed} replayed, echo present, auth enforced`);

// Negative: wrong key must fail the connection.
try {
  await withRunConnection({ ...target, secretKey: "wrong" }, {}, async (conn) => {
    await conn.newSession();
  });
  throw new Error("connection with wrong key unexpectedly succeeded");
} catch (err) {
  if (err instanceof Error && err.message.includes("unexpectedly")) throw err;
  console.log(`OK: wrong X-Secret-Key rejected (${(err as Error).constructor.name})`);
}
