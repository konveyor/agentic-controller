import { useCallback, useEffect, useRef, useState } from "react";
import {
  Alert,
  Bullseye,
  Button,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  PageSection,
  ProgressStep,
  ProgressStepper,
  Spinner,
  Title,
} from "@patternfly/react-core";
import ArrowLeftIcon from "@patternfly/react-icons/dist/esm/icons/arrow-left-icon";
import type {
  AgentPlaybookRun,
  AgentPlaybookRunStage,
} from "@konveyor/agentic-client/contract";
import type { ShimClient } from "@konveyor/agentic-client/transport-shim";
import { errorMessage, formatAge } from "../format";
import { PhaseLabel } from "./PhaseLabel";
import { playbookDuration } from "./PlaybookRunsPage";

const POLL_MS = 2_000;

/**
 * Map a stage phase to a ProgressStepper variant. Ready=False on the run
 * while a stage executes is the normal healthy state — only Failed is an
 * error here.
 */
function stepVariant(phase: AgentPlaybookRunStage["phase"]) {
  switch (phase) {
    case "Succeeded":
      return "success" as const;
    case "Failed":
      return "danger" as const;
    case "Running":
      return "info" as const;
    default:
      return "pending" as const;
  }
}

interface PlaybookRunDetailPageProps {
  api: ShimClient;
  name: string;
  onBack: () => void;
  /** Open a stage's AgentRun in the existing run detail page (ACP chat included). */
  onOpenRun: (runName: string) => void;
}

export function PlaybookRunDetailPage({ api, name, onBack, onOpenRun }: PlaybookRunDetailPageProps) {
  const [run, setRun] = useState<AgentPlaybookRun | null>(null);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [gone, setGone] = useState(false);
  const inFlight = useRef(false);

  const refresh = useCallback(async () => {
    if (inFlight.current || gone) return;
    inFlight.current = true;
    try {
      setRun(await api.getPlaybookRun(name));
      setFetchError(null);
    } catch (err) {
      const msg = errorMessage(err);
      if (msg.includes("404") || msg.toLowerCase().includes("not found")) {
        setGone(true);
      }
      setFetchError(msg);
    } finally {
      inFlight.current = false;
    }
  }, [api, name, gone]);

  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), POLL_MS);
    return () => clearInterval(timer);
  }, [refresh]);

  const stages = run?.status?.stages ?? [];
  const duration = run ? playbookDuration(run) : undefined;

  return (
    <PageSection>
      <Button variant="link" isInline icon={<ArrowLeftIcon />} onClick={onBack}>
        Back to playbook runs
      </Button>
      <Title headingLevel="h2" size="xl" style={{ margin: "0.5rem 0" }}>
        {name} <PhaseLabel phase={run?.status?.phase} />
      </Title>

      {fetchError && (
        <Alert
          variant={gone ? "warning" : "danger"}
          isInline
          title={gone ? "Playbook run no longer exists" : "Cannot load playbook run"}
          style={{ margin: "1rem 0" }}
        >
          {gone
            ? "It was deleted; its stage AgentRuns are owner-referenced and garbage-collected with it."
            : fetchError}
        </Alert>
      )}

      {run === null && !fetchError ? (
        <Bullseye>
          <Spinner aria-label="Loading playbook run" />
        </Bullseye>
      ) : run !== null ? (
        <>
          <DescriptionList columnModifier={{ default: "2Col" }} isCompact style={{ margin: "1rem 0" }}>
            <DescriptionListGroup>
              <DescriptionListTerm>Playbook</DescriptionListTerm>
              <DescriptionListDescription>{run.spec.playbookRef}</DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>Current stage</DescriptionListTerm>
              <DescriptionListDescription>
                {run.status?.currentStage ?? "—"}
              </DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>Age</DescriptionListTerm>
              <DescriptionListDescription>
                {formatAge(run.metadata.creationTimestamp)}
              </DescriptionListDescription>
            </DescriptionListGroup>
            <DescriptionListGroup>
              <DescriptionListTerm>Duration</DescriptionListTerm>
              <DescriptionListDescription>
                {duration !== undefined ? `${Math.floor(duration / 60)}m${String(duration % 60).padStart(2, "0")}s` : "—"}
              </DescriptionListDescription>
            </DescriptionListGroup>
          </DescriptionList>

          <Title headingLevel="h3" size="lg" style={{ margin: "1rem 0 0.5rem" }}>
            Stages
          </Title>
          {stages.length === 0 ? (
            <Alert variant="info" isInline title="No stage status yet">
              The controller has not populated stage status — the playbook may not be Ready.
            </Alert>
          ) : (
            <ProgressStepper aria-label="Playbook stages">
              {stages.map((stage) => (
                <ProgressStep
                  key={stage.name}
                  variant={stepVariant(stage.phase)}
                  isCurrent={stage.name === run.status?.currentStage}
                  id={`stage-${stage.name}`}
                  titleId={`stage-${stage.name}-title`}
                  aria-label={`stage ${stage.name}: ${stage.phase}`}
                  description={
                    stage.agentRunName ? (
                      <Button
                        variant="link"
                        isInline
                        onClick={() => onOpenRun(stage.agentRunName!)}
                      >
                        {stage.phase === "Running" ? "Open live run" : "Open run"}
                      </Button>
                    ) : (
                      stage.phase
                    )
                  }
                >
                  {stage.name}
                </ProgressStep>
              ))}
            </ProgressStepper>
          )}
        </>
      ) : null}
    </PageSection>
  );
}
