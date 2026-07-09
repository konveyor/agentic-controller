import { useEffect, useState } from "react";
import {
  Alert,
  Button,
  Form,
  FormGroup,
  FormHelperText,
  FormSelect,
  FormSelectOption,
  HelperText,
  HelperTextItem,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalVariant,
  Spinner,
  TextArea,
  TextInput,
} from "@patternfly/react-core";
import {
  CREDENTIAL_SOURCES_ANNOTATION,
  SOURCE_APPLICATION_IDENTITY,
  SOURCE_APPLICATION_REPOSITORY_BRANCH,
  SOURCE_APPLICATION_REPOSITORY_URL,
  parseSourcesAnnotation,
} from "@konveyor/agentic-client/contract";
import type { AgentParam, AgentResource, Application } from "@konveyor/agentic-client/contract";
import type { ShimClient } from "@konveyor/agentic-client/transport-shim";
import { errorMessage, truncate } from "../format";

/**
 * Human names for the source identifiers this UI recognizes. Membership IS
 * the recognition test (ADR 0005 fail-open): a param whose source is absent
 * here is treated as caller-supplied and gets a form field, so a newer agent
 * stays usable from an older UI.
 *
 * PARAM and CREDENTIAL vocabularies are separate on purpose. They resolve to
 * different value types (string vs Secret), and the platform recognizes each
 * identifier in only one of the two roles. Treating an identity source as a
 * recognized *param* source would hide a param the platform then declines to
 * resolve — a required param, silently empty.
 */
const PARAM_SOURCE_LABELS: Record<string, string> = {
  [SOURCE_APPLICATION_REPOSITORY_URL]: "application repository URL",
  [SOURCE_APPLICATION_REPOSITORY_BRANCH]: "application repository branch",
};

const CREDENTIAL_SOURCE_LABELS: Record<string, string> = {
  [SOURCE_APPLICATION_IDENTITY]: "application identity",
};

const isRecognizedParamSource = (source: string | undefined): boolean =>
  source !== undefined && Object.prototype.hasOwnProperty.call(PARAM_SOURCE_LABELS, source);

const isRecognizedCredentialSource = (source: string): boolean =>
  Object.prototype.hasOwnProperty.call(CREDENTIAL_SOURCE_LABELS, source);

/** Mirror of the platform's resolution, for previewing values in the form. */
function previewValue(source: string, app: Application | undefined): string | undefined {
  if (!app) return undefined;
  if (source === SOURCE_APPLICATION_REPOSITORY_URL) return app.repository?.url;
  if (source === SOURCE_APPLICATION_REPOSITORY_BRANCH) return app.repository?.branch;
  return undefined;
}

interface CreateRunModalProps {
  api: ShimClient;
  onClose: () => void;
  onCreated: (runName: string) => void;
}

function defaultsFor(agent: AgentResource | undefined): Record<string, string> {
  const values: Record<string, string> = {};
  for (const p of agent?.spec.params ?? []) {
    values[p.name] = p.default ?? "";
  }
  return values;
}

function paramHelperText(p: AgentParam): string {
  const parts: string[] = [];
  if (p.description) parts.push(p.description);
  if (p.type && p.type !== "string") parts.push(`type: ${p.type}`);
  if (p.default) parts.push(`default: ${p.default}`);
  return parts.join(" — ");
}

export function CreateRunModal({ api, onClose, onCreated }: CreateRunModalProps) {
  const [agents, setAgents] = useState<AgentResource[] | null>(null);
  const [agentsError, setAgentsError] = useState<string | null>(null);
  const [agentName, setAgentName] = useState("");
  const [applications, setApplications] = useState<Application[]>([]);
  const [applicationsError, setApplicationsError] = useState<string | null>(null);
  const [inventorySource, setInventorySource] = useState<"hub" | "stub" | "unknown" | null>(null);
  const [inventoryEndpoint, setInventoryEndpoint] = useState("");
  const [reloadingApps, setReloadingApps] = useState(false);
  const [applicationId, setApplicationId] = useState("");
  const [paramValues, setParamValues] = useState<Record<string, string>>({});
  const [instructions, setInstructions] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  useEffect(() => {
    let disposed = false;
    api
      .listAgents()
      .then((list) => {
        if (disposed) return;
        setAgents(list);
        const first = list.length > 0 ? list[0] : undefined;
        if (first?.metadata.name) {
          setAgentName(first.metadata.name);
          setParamValues(defaultsFor(first));
        }
      })
      .catch((err) => {
        if (!disposed) setAgentsError(errorMessage(err));
      });
    // A failing inventory must not block agents without sources — but for
    // agents that need one, the error is surfaced (see the alert below)
    // rather than leaving a dead form with a disabled Create button.
    api
      .listApplicationsWithSource()
      .then(({ source, endpoint, applications: list }) => {
        if (disposed) return;
        setApplications(list);
        setInventorySource(source);
        setInventoryEndpoint(endpoint);
      })
      .catch((err) => {
        if (!disposed) setApplicationsError(errorMessage(err));
      });
    return () => {
      disposed = true;
    };
  }, [api]);

  // Re-fetch the inventory on demand — register an application in Hub and
  // click Refresh to watch it appear, proving the list is live, not baked in.
  const reloadApplications = async () => {
    setReloadingApps(true);
    setApplicationsError(null);
    try {
      const { source, endpoint, applications: list } = await api.listApplicationsWithSource();
      setApplications(list);
      setInventorySource(source);
      setInventoryEndpoint(endpoint);
    } catch (err) {
      setApplicationsError(errorMessage(err));
    } finally {
      setReloadingApps(false);
    }
  };

  const selected = agents?.find((a) => a.metadata.name === agentName);

  const selectAgent = (name: string) => {
    setAgentName(name);
    setParamValues(defaultsFor(agents?.find((a) => a.metadata.name === name)));
  };

  // Partition params: those with a RECOGNIZED source are the platform's job
  // (given an application); everything else — including params whose source
  // this UI does not understand — is a user form field (ADR 0005 fail-open).
  const paramSources = parseSourcesAnnotation(selected);
  const credentialSources = parseSourcesAnnotation(selected, CREDENTIAL_SOURCES_ANNOTATION);
  const allParams = selected?.spec.params ?? [];
  const userParams = allParams.filter((p) => !isRecognizedParamSource(paramSources[p.name]));
  const platformParams = allParams.filter((p) => isRecognizedParamSource(paramSources[p.name]));
  const platformCredentials = Object.entries(credentialSources).filter(([, s]) =>
    isRecognizedCredentialSource(s),
  );
  const needsApplication = platformParams.length > 0 || platformCredentials.length > 0;
  const application = applications.find((a) => a.id === applicationId);

  const missingRequired = userParams.filter((p) => p.required && !(paramValues[p.name] ?? "").trim());
  const missingApplication = needsApplication && !application;
  // A required sourced param the chosen application cannot supply would be a
  // 400 from the platform. Catch it here so Create is disabled with a reason
  // rather than failing on submit with an error the user cannot act on.
  const unresolvable = application
    ? platformParams.filter(
        (p) => p.required && !p.default && !previewValue(paramSources[p.name], application),
      )
    : [];
  const canCreate =
    !!selected &&
    missingRequired.length === 0 &&
    !missingApplication &&
    unresolvable.length === 0 &&
    !submitting;

  const submit = async () => {
    if (!selected || !canCreate) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const params: Record<string, string> = {};
      for (const p of userParams) {
        const v = (paramValues[p.name] ?? "").trim();
        if (v) params[p.name] = v; // omit empty optional params
      }
      const created = await api.createRun({
        agentRef: selected.metadata.name ?? agentName,
        params: Object.keys(params).length > 0 ? params : undefined,
        instructions: instructions.trim() || undefined,
        applicationRef: needsApplication ? application?.id : undefined,
      });
      const name = created.metadata.name;
      if (!name) throw new Error("shim returned a created run without metadata.name");
      onCreated(name);
    } catch (err) {
      setSubmitError(errorMessage(err));
      setSubmitting(false);
    }
  };

  return (
    <Modal
      variant={ModalVariant.medium}
      isOpen
      onClose={() => {
        if (!submitting) onClose();
      }}
      aria-labelledby="create-run-title"
    >
      <ModalHeader
        title="Create run"
        labelId="create-run-title"
        description="Creates an AgentRun; the controller provisions a sandbox pod running the agent's ACP server."
      />
      <ModalBody>
        {agentsError && (
          <Alert variant="danger" isInline title="Failed to load agents" style={{ marginBottom: "1rem" }}>
            {agentsError}
          </Alert>
        )}
        {submitError && (
          <Alert variant="danger" isInline title="Create failed" style={{ marginBottom: "1rem" }}>
            {submitError}
          </Alert>
        )}
        {agents === null && !agentsError ? (
          <Spinner aria-label="Loading agents" />
        ) : agents !== null && agents.length === 0 ? (
          <Alert variant="warning" isInline title="No Agent resources found">
            The cluster has no Agent CRs in the shim's namespace, so there is nothing to run.
          </Alert>
        ) : (
          <Form
            id="create-run-form"
            onSubmit={(e) => {
              e.preventDefault();
              void submit();
            }}
          >
            <FormGroup label="Agent" isRequired fieldId="create-agent">
              <FormSelect
                id="create-agent"
                value={agentName}
                onChange={(_e, v) => selectAgent(v)}
              >
                {(agents ?? []).map((a) => (
                  <FormSelectOption
                    key={a.metadata.name}
                    value={a.metadata.name}
                    label={a.metadata.name ?? "(unnamed)"}
                  />
                ))}
              </FormSelect>
              {selected?.spec.prompt && (
                <FormHelperText>
                  <HelperText>
                    <HelperTextItem>{truncate(selected.spec.prompt, 160)}</HelperTextItem>
                  </HelperText>
                </FormHelperText>
              )}
            </FormGroup>

            {needsApplication && applicationsError && (
              <Alert variant="danger" isInline title="Failed to load applications">
                {applicationsError} — this agent resolves its inputs from an application, so a run
                cannot be created until the inventory loads.
              </Alert>
            )}
            {needsApplication && !applicationsError && applications.length === 0 && (
              <Alert variant="warning" isInline title="No applications available">
                This agent resolves its inputs from an application, but the platform's inventory is
                empty.
              </Alert>
            )}

            {needsApplication && inventorySource && (
              <div className={`inventory-source inventory-source-${inventorySource}`}>
                <span className="inventory-source-label">
                  {inventorySource === "hub"
                    ? `${applications.length} application${applications.length === 1 ? "" : "s"} from Konveyor Hub`
                    : inventorySource === "stub"
                      ? "Konveyor Hub unavailable — showing offline stub"
                      : "Application inventory"}
                </span>
                <code className="inventory-source-endpoint">{inventoryEndpoint}</code>
                <Button
                  variant="link"
                  isInline
                  isLoading={reloadingApps}
                  isDisabled={reloadingApps}
                  onClick={() => void reloadApplications()}
                >
                  Refresh
                </Button>
              </div>
            )}

            {needsApplication && (
              <FormGroup label="Application" isRequired fieldId="create-application">
                <FormSelect
                  id="create-application"
                  value={applicationId}
                  onChange={(_e, v) => setApplicationId(v)}
                >
                  <FormSelectOption value="" label="Select an application…" isDisabled />
                  {applications.map((a) => (
                    <FormSelectOption key={a.id} value={a.id} label={`${a.name}  ·  Hub #${a.id}`} />
                  ))}
                </FormSelect>
                <FormHelperText>
                  <HelperText>
                    <HelperTextItem>
                      This agent takes its inputs from an application; the platform resolves them
                      on create.
                    </HelperTextItem>
                  </HelperText>
                </FormHelperText>
              </FormGroup>
            )}

            {needsApplication && unresolvable.length > 0 && (
              <Alert
                variant="warning"
                isInline
                title={`${application?.name ?? "This application"} cannot supply every required input`}
              >
                No value for {unresolvable.map((p) => p.name).join(", ")}. Choose an application that
                has one, or ask an administrator to complete this application's record.
              </Alert>
            )}

            {needsApplication && (
              <dl
                className="resolved-params"
                role="group"
                aria-label="Inputs the platform resolves from the selected application"
              >
                {platformParams.map((p) => {
                  const source = paramSources[p.name];
                  const value = previewValue(source, application);
                  return (
                    <div key={p.name} className="resolved-param">
                      <dt>
                        <code>{p.name}</code>
                      </dt>
                      <dd className="resolved-param-source">
                        <span aria-hidden="true">← </span>
                        from {PARAM_SOURCE_LABELS[source]}
                        {value ? (
                          <span className="resolved-param-value">{truncate(value, 60)}</span>
                        ) : application ? (
                          <span className="resolved-param-value resolved-param-missing">
                            no value on this application
                          </span>
                        ) : null}
                      </dd>
                    </div>
                  );
                })}
                {platformCredentials.map(([name, source]) => (
                  <div key={`cred-${name}`} className="resolved-param">
                    <dt>
                      <code>{name} credentials</code>
                    </dt>
                    <dd className="resolved-param-source">
                      <span aria-hidden="true">← </span>
                      from {CREDENTIAL_SOURCE_LABELS[source]}
                      {application &&
                        (application.identity ? (
                          <span className="resolved-param-value">
                            Hub identity: {application.identity.name}
                            {application.identitySecret ? (
                              <> → {application.identitySecret}</>
                            ) : (
                              <span className="resolved-param-missing"> (materialization pending)</span>
                            )}
                          </span>
                        ) : (
                          <span className="resolved-param-value">none on this application</span>
                        ))}
                    </dd>
                  </div>
                ))}
              </dl>
            )}

            {userParams.map((p) => {
              const helper = paramHelperText(p);
              return (
                <FormGroup key={p.name} label={p.name} isRequired={p.required} fieldId={`param-${p.name}`}>
                  <TextInput
                    id={`param-${p.name}`}
                    isRequired={p.required}
                    value={paramValues[p.name] ?? ""}
                    onChange={(_e, v) => setParamValues((prev) => ({ ...prev, [p.name]: v }))}
                  />
                  {helper && (
                    <FormHelperText>
                      <HelperText>
                        <HelperTextItem>{helper}</HelperTextItem>
                      </HelperText>
                    </FormHelperText>
                  )}
                </FormGroup>
              );
            })}

            <FormGroup label="Instructions" fieldId="create-instructions">
              <TextArea
                id="create-instructions"
                value={instructions}
                onChange={(_e, v) => setInstructions(v)}
                rows={4}
                resizeOrientation="vertical"
                placeholder="Task-specific instructions, composed with the agent's standing prompt"
              />
            </FormGroup>
          </Form>
        )}
      </ModalBody>
      <ModalFooter>
        <Button variant="primary" isDisabled={!canCreate} isLoading={submitting} onClick={() => void submit()}>
          Create
        </Button>
        <Button variant="link" isDisabled={submitting} onClick={onClose}>
          Cancel
        </Button>
      </ModalFooter>
    </Modal>
  );
}
