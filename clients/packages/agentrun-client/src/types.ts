/**
 * TypeScript mirrors of the konveyor.io/v1alpha1 CRD types.
 *
 * Source of truth: github.com/konveyor/agentic-controller api/v1alpha1/*.go
 * Keep field names and optionality in sync with the Go structs.
 */

export const GROUP = "konveyor.io";
export const VERSION = "v1alpha1";
export const API_VERSION = `${GROUP}/${VERSION}`;

export interface ObjectMeta {
  name?: string;
  generateName?: string;
  namespace?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  uid?: string;
  resourceVersion?: string;
  creationTimestamp?: string;
}

export interface Condition {
  type: string;
  status: "True" | "False" | "Unknown";
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
  observedGeneration?: number;
}

export interface LocalObjectReference {
  name: string;
}

export interface EnvVar {
  name: string;
  value?: string;
  valueFrom?: unknown;
}

export interface EnvFromSource {
  configMapRef?: LocalObjectReference & { optional?: boolean };
  secretRef?: LocalObjectReference & { optional?: boolean };
  prefix?: string;
}

// ---------------------------------------------------------------- AgentRun

export type AgentRunPhase = "Pending" | "Running" | "Succeeded" | "Failed";

export interface AgentRunModelSelection {
  /** Purpose of this model in the run, e.g. "primary", "efficient". */
  role: string;
  /** Name of an LLMProvider CR; must be in the Agent's providers list. */
  provider: string;
  /** Model identifier declared on the referenced LLMProvider. */
  model: string;
}

export interface AgentRunParam {
  /** Matches an Agent param declaration. */
  name: string;
  value: string;
}

export interface AgentRunSpec {
  /** Name of the Agent CR to execute. Immutable. */
  agentRef: string;
  models?: AgentRunModelSelection[];
  /** Injected as KONVEYOR_PARAM_{NAME} env vars into the Sandbox. */
  params?: AgentRunParam[];
  /** Task-specific instructions, composed with the Agent's prompt. */
  instructions?: string;
  env?: EnvVar[];
  envFrom?: EnvFromSource[];
}

export interface AgentRunStatus {
  phase?: AgentRunPhase;
  observedGeneration?: number;
  /** Name of the Sandbox CR created for this run. */
  sandboxName?: string;
  startTime?: string;
  completionTime?: string;
  /** Wall-clock duration of the run in seconds. */
  duration?: number;
  /**
   * References a Secret containing the ACP authentication key for
   * connecting to the agent's ACP endpoint (goose serve, port 4000).
   */
  secretKeyRef?: LocalObjectReference;
  conditions?: Condition[];
}

export interface AgentRun {
  apiVersion: typeof API_VERSION;
  kind: "AgentRun";
  metadata: ObjectMeta;
  spec: AgentRunSpec;
  status?: AgentRunStatus;
}

// ------------------------------------------------------------------- Agent

export type AgentParamType = "string" | "number" | "boolean";

export interface AgentParam {
  name: string;
  type?: AgentParamType;
  description?: string;
  default?: string;
  required?: boolean;
}

export interface AgentSpec {
  /** Container image carrying the agent runtime and toolchains. */
  image: string;
  /** Standing instructions, composed with AgentRun instructions. */
  prompt?: string;
  providers: { ref: string }[];
  skillCards?: { ref: string }[];
  skillCollections?: { ref: string }[];
  params?: AgentParam[];
}

export interface Agent {
  apiVersion: typeof API_VERSION;
  kind: "Agent";
  metadata: ObjectMeta;
  spec: AgentSpec;
  status?: { observedGeneration?: number; conditions?: Condition[] };
}

// ------------------------------------------------------------- LLMProvider

export interface LLMProviderModel {
  name: string;
  contextWindow: number;
  tier?: string;
}

export interface LLMProviderSpec {
  endpoint: string;
  credentialRef: { secretName: string; key: string };
  models: LLMProviderModel[];
}

export interface LLMProvider {
  apiVersion: typeof API_VERSION;
  kind: "LLMProvider";
  metadata: ObjectMeta;
  spec: LLMProviderSpec;
  status?: {
    observedGeneration?: number;
    connectionVerified?: boolean;
    discoveredModels?: string[];
    conditions?: Condition[];
  };
}

// --------------------------------------------------------------- SkillCard

export interface SkillCardSpec {
  displayName?: string;
  description?: string;
  /** Exactly one of image | inline | source provides the skill content. */
  image?: string;
  inline?: string;
  source?: string;
  tags?: string[];
  type?: string;
  version?: string;
}

export interface SkillCard {
  apiVersion: typeof API_VERSION;
  kind: "SkillCard";
  metadata: ObjectMeta;
  spec: SkillCardSpec;
  status?: {
    observedGeneration?: number;
    resolvedImage?: string;
    conditions?: Condition[];
  };
}

// --------------------------------------------------------- SkillCollection

export interface SkillCollectionSkillRef {
  /** Local name for this skill within the collection. */
  name: string;
  /** Exactly one of skillCardRef | image | source must be set. */
  skillCardRef?: string;
  image?: string;
  source?: string;
}

export interface SkillCollectionSpec {
  skills?: SkillCollectionSkillRef[];
}

export interface SkillCollection {
  apiVersion: typeof API_VERSION;
  kind: "SkillCollection";
  metadata: ObjectMeta;
  spec: SkillCollectionSpec;
  status?: { observedGeneration?: number; conditions?: Condition[] };
}

export const PLURALS = {
  AgentRun: "agentruns",
  Agent: "agents",
  LLMProvider: "llmproviders",
  SkillCard: "skillcards",
  SkillCollection: "skillcollections",
} as const;
