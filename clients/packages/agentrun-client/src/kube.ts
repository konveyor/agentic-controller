/**
 * Kubernetes-side client for AgentRun CRs.
 *
 * This is the direct-to-apiserver path (kubeconfig auth) blessed by ADR 0002
 * for clients without the web UI. In an IDE we know the workspace's git
 * remote/branch already, so we build the CR ourselves rather than going
 * through Hub's smart-create (ADR 0003). Swapping this class for a Hub REST
 * implementation later only changes the transport, not the interface.
 */
import * as k8s from "@kubernetes/client-node";
import {
  API_VERSION,
  GROUP,
  VERSION,
  PLURALS,
  type Agent,
  type AgentRun,
  type AgentRunSpec,
} from "./types.js";

export interface AcpEndpoint {
  /** Pod running the agent (port-forward target). */
  podName: string;
  /** Stable DNS name inside the cluster: <sandboxName>.<namespace>.svc */
  serviceHost: string;
  port: number;
  /** Value for the X-Secret-Key header. */
  secretKey: string;
}

export interface WaitOptions {
  timeoutMs?: number;
  pollIntervalMs?: number;
  signal?: AbortSignal;
}

/**
 * Secret data keys the ACP key may be stored under, newest-first: the real
 * agentic-controller uses "secret-key"; the dev simulator used "ACP_SECRET_KEY".
 */
const SECRET_DATA_KEYS = ["secret-key", "ACP_SECRET_KEY"];
const ACP_PORT = 4000;

export class AgentRunClient {
  readonly kc: k8s.KubeConfig;
  readonly namespace: string;
  private readonly custom: k8s.CustomObjectsApi;
  private readonly core: k8s.CoreV1Api;

  constructor(options?: { kubeConfig?: k8s.KubeConfig; namespace?: string }) {
    this.kc = options?.kubeConfig ?? new k8s.KubeConfig();
    if (!options?.kubeConfig) this.kc.loadFromDefault();
    this.namespace = options?.namespace ?? "konveyor-agents";
    this.custom = this.kc.makeApiClient(k8s.CustomObjectsApi);
    this.core = this.kc.makeApiClient(k8s.CoreV1Api);
  }

  async getAgent(name: string): Promise<Agent> {
    return (await this.custom.getNamespacedCustomObject({
      group: GROUP,
      version: VERSION,
      namespace: this.namespace,
      plural: PLURALS.Agent,
      name,
    })) as Agent;
  }

  async createAgentRun(
    spec: AgentRunSpec,
    options?: { name?: string; generateName?: string; labels?: Record<string, string> },
  ): Promise<AgentRun> {
    const body: AgentRun = {
      apiVersion: API_VERSION,
      kind: "AgentRun",
      metadata: {
        ...(options?.name
          ? { name: options.name }
          : { generateName: options?.generateName ?? `${spec.agentRef}-` }),
        namespace: this.namespace,
        labels: options?.labels,
      },
      spec,
    };
    return (await this.custom.createNamespacedCustomObject({
      group: GROUP,
      version: VERSION,
      namespace: this.namespace,
      plural: PLURALS.AgentRun,
      body,
    })) as AgentRun;
  }

  async getAgentRun(name: string): Promise<AgentRun> {
    return (await this.custom.getNamespacedCustomObject({
      group: GROUP,
      version: VERSION,
      namespace: this.namespace,
      plural: PLURALS.AgentRun,
      name,
    })) as AgentRun;
  }

  async deleteAgentRun(name: string): Promise<void> {
    await this.custom.deleteNamespacedCustomObject({
      group: GROUP,
      version: VERSION,
      namespace: this.namespace,
      plural: PLURALS.AgentRun,
      name,
    });
  }

  /**
   * Waits until `predicate` returns truthy for the run, polling the
   * apiserver. Polling keeps this dependency-free and works on every
   * cluster; swap in a Watch if churn matters.
   */
  async waitFor<T>(
    name: string,
    predicate: (run: AgentRun) => T | undefined | false,
    options?: WaitOptions,
  ): Promise<T> {
    const timeoutMs = options?.timeoutMs ?? 120_000;
    const interval = options?.pollIntervalMs ?? 1_000;
    const deadline = Date.now() + timeoutMs;
    for (;;) {
      options?.signal?.throwIfAborted();
      const run = await this.getAgentRun(name);
      const result = predicate(run);
      if (result) return result;
      if (run.status?.phase === "Failed") {
        const msg = run.status.conditions?.map((c) => c.message).filter(Boolean).join("; ");
        throw new Error(`AgentRun ${name} failed${msg ? `: ${msg}` : ""}`);
      }
      if (Date.now() > deadline) {
        throw new Error(
          `Timed out after ${timeoutMs}ms waiting for AgentRun ${name} (phase=${run.status?.phase ?? "unset"})`,
        );
      }
      await new Promise((r) => setTimeout(r, interval));
    }
  }

  /**
   * Waits for the run to be connectable (Running, sandbox up, ACP key
   * published) and resolves the pieces needed to open an ACP connection.
   */
  async waitForAcpEndpoint(name: string, options?: WaitOptions): Promise<AcpEndpoint> {
    const ready = await this.waitFor(
      name,
      (run) =>
        run.status?.phase === "Running" &&
        run.status.sandboxName &&
        run.status.secretKeyRef?.name
          ? { sandboxName: run.status.sandboxName, secretName: run.status.secretKeyRef.name }
          : undefined,
      options,
    );

    const secret = await this.core.readNamespacedSecret({
      name: ready.secretName,
      namespace: this.namespace,
    });
    const data = secret.data ?? {};
    // Prefer a known key; tolerate a harness that picked another name as long
    // as the secret holds exactly one entry.
    const b64 =
      SECRET_DATA_KEYS.map((k) => data[k]).find((v) => v !== undefined) ??
      (Object.keys(data).length === 1 ? Object.values(data)[0] : undefined);
    if (!b64) {
      throw new Error(
        `Secret ${ready.secretName} has no ACP key (looked for ${SECRET_DATA_KEYS.join(
          ", ",
        )}; keys present: ${Object.keys(data).join(", ")})`,
      );
    }

    // Both the real Agent Sandbox controller and the dev simulator name the
    // backing pod after the Sandbox, so resolve it by name first. Fall back to
    // the run label — the real controller does NOT put konveyor.io/agentrun on
    // the pod (only agents.x-k8s.io/sandbox-name-hash), so name-first is what
    // makes this work against agentic-controller.
    let podName = await this.core
      .readNamespacedPod({ name: ready.sandboxName, namespace: this.namespace })
      .then((p) => p.metadata?.name)
      .catch(() => undefined);
    if (!podName) {
      const pods = await this.core.listNamespacedPod({
        namespace: this.namespace,
        labelSelector: `konveyor.io/agentrun=${name}`,
      });
      const pod = pods.items.find((p) => p.status?.phase === "Running") ?? pods.items[0];
      podName = pod?.metadata?.name;
    }
    if (!podName) {
      throw new Error(`No sandbox pod found for AgentRun ${name} (sandbox ${ready.sandboxName})`);
    }

    return {
      podName,
      serviceHost: `${ready.sandboxName}.${this.namespace}.svc`,
      port: ACP_PORT,
      secretKey: Buffer.from(b64, "base64").toString("utf8"),
    };
  }
}
