import { Label } from "@patternfly/react-core";
import type { AgentRunPhase } from "@konveyor/agentic-client/contract";

const PHASE_COLOR: Record<AgentRunPhase, "grey" | "blue" | "green" | "red"> = {
  Pending: "grey",
  Running: "blue",
  Succeeded: "green",
  Failed: "red",
};

/** Colored phase label; unset phase renders as Pending (grey). */
export function PhaseLabel({ phase }: { phase?: string }) {
  const shown = phase ?? "Pending";
  const color = PHASE_COLOR[shown as AgentRunPhase] ?? "grey";
  return <Label color={color}>{shown}</Label>;
}
