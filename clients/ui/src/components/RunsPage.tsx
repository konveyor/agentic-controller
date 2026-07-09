import { useCallback, useEffect, useRef, useState } from "react";
import {
  Alert,
  Bullseye,
  Button,
  EmptyState,
  EmptyStateActions,
  EmptyStateBody,
  EmptyStateFooter,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalVariant,
  PageSection,
  Spinner,
  Title,
  Toolbar,
  ToolbarContent,
  ToolbarItem,
} from "@patternfly/react-core";
import { ActionsColumn, Table, Tbody, Td, Th, Thead, Tr } from "@patternfly/react-table";
import CubesIcon from "@patternfly/react-icons/dist/esm/icons/cubes-icon";
import type { AgentRun } from "@konveyor/agentic-client/contract";
import type { ShimClient } from "@konveyor/agentic-client/transport-shim";
import { errorMessage, formatAge, formatDuration } from "../format";
import { PhaseLabel } from "./PhaseLabel";
import { CreateRunModal } from "./CreateRunModal";

const POLL_MS = 2_000;

interface RunsPageProps {
  api: ShimClient;
  onOpenRun: (runName: string) => void;
}

export function RunsPage({ api, onOpenRun }: RunsPageProps) {
  const [runs, setRuns] = useState<AgentRun[] | null>(null);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [isCreateOpen, setCreateOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const inFlight = useRef(false);

  const refresh = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    try {
      const list = await api.listRuns();
      list.sort((a, b) =>
        (b.metadata.creationTimestamp ?? "").localeCompare(a.metadata.creationTimestamp ?? ""),
      );
      setRuns(list);
      setFetchError(null);
    } catch (err) {
      setFetchError(errorMessage(err));
    } finally {
      inFlight.current = false;
    }
  }, [api]);

  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), POLL_MS);
    return () => clearInterval(timer);
  }, [refresh]);

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    setDeleteError(null);
    try {
      await api.deleteRun(deleteTarget);
      setDeleteTarget(null);
      void refresh();
    } catch (err) {
      setDeleteError(errorMessage(err));
    } finally {
      setDeleting(false);
    }
  };

  return (
    <>
      <PageSection>
        <Title headingLevel="h2" size="xl">
          Agent runs
        </Title>
        <Toolbar inset={{ default: "insetNone" }}>
          <ToolbarContent>
            <ToolbarItem>
              <Button variant="primary" onClick={() => setCreateOpen(true)}>
                Create run
              </Button>
            </ToolbarItem>
          </ToolbarContent>
        </Toolbar>
        {fetchError && (
          <Alert
            variant="danger"
            isInline
            title="Cannot reach the hub-shim"
            style={{ marginBottom: "1rem" }}
          >
            {fetchError}
          </Alert>
        )}
        {runs === null && !fetchError ? (
          <Bullseye>
            <Spinner aria-label="Loading runs" />
          </Bullseye>
        ) : runs !== null && runs.length === 0 ? (
          <EmptyState titleText="No agent runs" headingLevel="h3" icon={CubesIcon}>
            <EmptyStateBody>
              No AgentRuns exist yet. Create one to provision a sandbox and chat with the agent.
            </EmptyStateBody>
            <EmptyStateFooter>
              <EmptyStateActions>
                <Button variant="primary" onClick={() => setCreateOpen(true)}>
                  Create run
                </Button>
              </EmptyStateActions>
            </EmptyStateFooter>
          </EmptyState>
        ) : runs !== null ? (
          <Table aria-label="Agent runs" variant="compact">
            <Thead>
              <Tr>
                <Th>Name</Th>
                <Th>Agent</Th>
                <Th>Phase</Th>
                <Th>Age</Th>
                <Th>Duration</Th>
                <Th screenReaderText="Actions" />
              </Tr>
            </Thead>
            <Tbody>
              {runs.map((run) => {
                const name = run.metadata.name ?? "";
                return (
                  <Tr key={run.metadata.uid ?? name}>
                    <Td dataLabel="Name">
                      <Button variant="link" isInline onClick={() => onOpenRun(name)}>
                        {name}
                      </Button>
                    </Td>
                    <Td dataLabel="Agent">{run.spec.agentRef}</Td>
                    <Td dataLabel="Phase">
                      <PhaseLabel phase={run.status?.phase} />
                    </Td>
                    <Td dataLabel="Age">{formatAge(run.metadata.creationTimestamp)}</Td>
                    <Td dataLabel="Duration">{formatDuration(run.status?.duration)}</Td>
                    <Td isActionCell>
                      <ActionsColumn
                        items={[
                          {
                            title: "Delete",
                            onClick: () => {
                              setDeleteError(null);
                              setDeleteTarget(name);
                            },
                          },
                        ]}
                      />
                    </Td>
                  </Tr>
                );
              })}
            </Tbody>
          </Table>
        ) : null}
      </PageSection>

      {isCreateOpen && (
        <CreateRunModal
          api={api}
          onClose={() => setCreateOpen(false)}
          onCreated={(name) => {
            setCreateOpen(false);
            onOpenRun(name);
          }}
        />
      )}

      <Modal
        variant={ModalVariant.small}
        isOpen={deleteTarget !== null}
        onClose={() => {
          if (!deleting) setDeleteTarget(null);
        }}
        aria-labelledby="delete-run-title"
      >
        <ModalHeader title="Delete AgentRun?" labelId="delete-run-title" />
        <ModalBody>
          {deleteError && (
            <Alert
              variant="danger"
              isInline
              title="Delete failed"
              style={{ marginBottom: "1rem" }}
            >
              {deleteError}
            </Alert>
          )}
          Delete run <strong>{deleteTarget}</strong>? Its sandbox pod and ACP session go with it.
          AgentRun specs are immutable, so re-running means creating a new run.
        </ModalBody>
        <ModalFooter>
          <Button variant="danger" isLoading={deleting} isDisabled={deleting} onClick={() => void confirmDelete()}>
            Delete
          </Button>
          <Button variant="link" isDisabled={deleting} onClick={() => setDeleteTarget(null)}>
            Cancel
          </Button>
        </ModalFooter>
      </Modal>
    </>
  );
}
