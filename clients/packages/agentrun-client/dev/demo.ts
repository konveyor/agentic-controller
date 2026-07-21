/**
 * End-to-end demo of the VSCode-extension flow against minikube:
 *
 *   create AgentRun CR -> (simulator reconciles) -> wait for Running +
 *   secretKeyRef -> read key -> port-forward -> ACP over WebSocket ->
 *   session/new -> session/prompt (streamed) -> reconnect + session/load
 *   (history replay), proving the multi-client/reattach story.
 *
 * Run `npm run simulator` in another terminal first.
 */
import type * as acp from "@agentclientprotocol/sdk";
import { AgentRunClient } from "../src/kube.js";
import { openTunnel } from "../src/portforward.js";
import { withRunConnection, targetFromEndpoint } from "../src/acp.js";

const client = new AgentRunClient();

function renderUpdate(prefix: string) {
  return (n: acp.SessionNotification) => {
    const u = n.update;
    switch (u.sessionUpdate) {
      case "agent_message_chunk":
        if (u.content.type === "text") process.stdout.write(u.content.text);
        break;
      case "tool_call":
        console.log(`${prefix} [tool_call] ${u.title} (${u.status})`);
        break;
      case "tool_call_update":
        console.log(`${prefix} [tool_call_update] ${u.toolCallId} -> ${u.status}`);
        break;
      default:
        console.log(`${prefix} [${u.sessionUpdate}]`);
    }
  };
}

console.log("1. Creating AgentRun (the extension builds this from the open workspace)...");
const run = await client.createAgentRun(
  {
    agentRef: "migration-analyzer",
    instructions: "Analyze the coolstore app for EAP8 migration blockers.",
    params: [
      { name: "repository", value: "https://github.com/konveyor-ecosystem/coolstore.git" },
      // "branch" omitted on purpose: exercises Agent param defaults
    ],
  },
  { generateName: "demo-" },
);
const name = run.metadata.name!;
console.log(`   created AgentRun ${name}`);

console.log("2. Waiting for Running + secretKeyRef (simulator provisions the sandbox)...");
const endpoint = await client.waitForAcpEndpoint(name, { timeoutMs: 90_000 });
console.log(`   pod=${endpoint.podName} service=${endpoint.serviceHost}:${endpoint.port}`);

console.log("3. Opening port-forward tunnel...");
const tunnel = await openTunnel(client.kc, client.namespace, endpoint.podName, endpoint.port);
console.log(`   127.0.0.1:${tunnel.localPort} -> ${endpoint.podName}:${endpoint.port}`);

let savedSessionId = "";
try {
  const target = targetFromEndpoint(endpoint, tunnel.localPort);

  console.log("4. Connecting ACP over WebSocket (X-Secret-Key auth)...\n");
  await withRunConnection(target, { onSessionUpdate: renderUpdate("   live") }, async (conn) => {
    console.log(
      `   initialized: protocol v${conn.initialized.protocolVersion}, loadSession=${conn.initialized.agentCapabilities?.loadSession}`,
    );
    const session = await conn.newSession();
    savedSessionId = session.sessionId;
    console.log(`   session ${savedSessionId}\n--- streamed output ---`);
    const result = await conn.prompt(savedSessionId, "Start the migration analysis.");
    console.log(`--- turn done: ${result.stopReason} ---\n`);
  });

  console.log("5. Reconnecting fresh + session/load (full history replay)...\n");
  let replayed = 0;
  await withRunConnection(
    target,
    { onSessionUpdate: () => void replayed++ },
    async (conn) => {
      await conn.loadSession(savedSessionId);
      console.log(`   replayed ${replayed} updates from session ${savedSessionId}`);
    },
  );

  console.log(`\nE2E OK. Inspect with:`);
  console.log(`  kubectl get agentrun ${name} -n ${client.namespace} -o yaml`);
  console.log(`  kubectl logs ${endpoint.podName} -n ${client.namespace}`);
} finally {
  tunnel.close();
}
