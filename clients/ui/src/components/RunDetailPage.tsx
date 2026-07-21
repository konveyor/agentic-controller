import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  PageSection,
  Title,
} from "@patternfly/react-core";
import ArrowLeftIcon from "@patternfly/react-icons/dist/esm/icons/arrow-left-icon";
import type { AgentRun } from "@konveyor/agentic-client/contract";
import type { ShimClient } from "@konveyor/agentic-client/transport-shim";
import { errorMessage, formatAge, formatDuration } from "../format";
import { PhaseLabel } from "./PhaseLabel";
import { ChatPanel } from "./ChatPanel";

const POLL_MS = 2_000;

interface RunDetailPageProps {
  api: ShimClient;
  runName: string;
  onBack: () => void;
}

export function RunDetailPage({ api, runName, onBack }: RunDetailPageProps) {
  const [run, setRun] = useState<AgentRun | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);

  useEffect(() => {
    let disposed = false;
    let inFlight = false;
    let stopped = false;
    const tick = async () => {
      if (inFlight || stopped) return;
      inFlight = true;
      try {
        const r = await api.getRun(runName);
        if (!disposed) {
          setRun(r);
          setError(null);
        }
      } catch (err) {
        if (!disposed) {
          const msg = errorMessage(err);
          if (msg.includes("HTTP 404")) {
            setNotFound(true);
            stopped = true; // gone for good — no point polling on
          } else {
            setError(msg);
          }
        }
      } finally {
        inFlight = false;
      }
    };
    void tick();
    const timer = setInterval(() => void tick(), POLL_MS);
    return () => {
      disposed = true;
      clearInterval(timer);
    };
  }, [api, runName]);

  if (notFound) {
    return (
      <PageSection>
        <Alert variant="warning" isInline title={`AgentRun "${runName}" not found`}>
          It may have been deleted.
        </Alert>
        <Button variant="link" isInline icon={<ArrowLeftIcon />} onClick={onBack} style={{ marginTop: "1rem" }}>
          Back to runs
        </Button>
      </PageSection>
    );
  }

  return (
    <>
      <PageSection>
        <Button variant="link" isInline icon={<ArrowLeftIcon />} onClick={onBack}>
          Back to runs
        </Button>
        <Flex
          alignItems={{ default: "alignItemsCenter" }}
          spaceItems={{ default: "spaceItemsMd" }}
          style={{ marginTop: "0.5rem" }}
        >
          <FlexItem>
            <Title headingLevel="h2" size="xl">
              {runName}
            </Title>
          </FlexItem>
          <FlexItem>
            <PhaseLabel phase={run?.status?.phase} />
          </FlexItem>
        </Flex>
        {error && (
          <Alert variant="danger" isInline title="Failed to refresh run" style={{ marginTop: "0.75rem" }}>
            {error}
          </Alert>
        )}
        <DescriptionList
          isHorizontal
          isCompact
          columnModifier={{ default: "2Col" }}
          style={{ marginTop: "0.75rem" }}
        >
          <DescriptionListGroup>
            <DescriptionListTerm>Agent</DescriptionListTerm>
            <DescriptionListDescription>{run?.spec.agentRef ?? "—"}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>Sandbox</DescriptionListTerm>
            <DescriptionListDescription>{run?.status?.sandboxName ?? "—"}</DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>Age</DescriptionListTerm>
            <DescriptionListDescription>
              {formatAge(run?.metadata.creationTimestamp)}
            </DescriptionListDescription>
          </DescriptionListGroup>
          <DescriptionListGroup>
            <DescriptionListTerm>Duration</DescriptionListTerm>
            <DescriptionListDescription>
              {formatDuration(run?.status?.duration)}
            </DescriptionListDescription>
          </DescriptionListGroup>
        </DescriptionList>
      </PageSection>
      <PageSection isFilled className="run-detail-chat-section">
        <ChatPanel api={api} runName={runName} />
      </PageSection>
    </>
  );
}
