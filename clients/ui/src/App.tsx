import { useMemo, useState } from "react";
import {
  Alert,
  Masthead,
  MastheadBrand,
  MastheadContent,
  MastheadMain,
  Page,
  PageSection,
  Title,
  ToggleGroup,
  ToggleGroupItem,
} from "@patternfly/react-core";
import { ShimClient } from "@konveyor/agentic-client/transport-shim";
import { errorMessage } from "./format";
import { RunsPage } from "./components/RunsPage";
import { RunDetailPage } from "./components/RunDetailPage";
import { PlaybookRunsPage } from "./components/PlaybookRunsPage";
import { PlaybookRunDetailPage } from "./components/PlaybookRunDetailPage";

// Dev default: the local shim. Production (static build behind nginx):
// same-origin — nginx proxies /api (HTTP + WebSocket) to the gateway.
const SHIM_URL =
  import.meta.env.VITE_SHIM_URL ??
  (import.meta.env.DEV ? "http://127.0.0.1:7080" : window.location.origin);

type View =
  | { kind: "list" }
  | { kind: "detail"; runName: string; fromPlaybookRun?: string }
  | { kind: "playbooks" }
  | { kind: "playbookDetail"; name: string };

export function App() {
  const [view, setView] = useState<View>({ kind: "list" });

  // ShimClient validates the base URL eagerly — surface a bad VITE_SHIM_URL
  // as an alert instead of a white screen.
  const api = useMemo<{ client?: ShimClient; error?: string }>(() => {
    try {
      return { client: new ShimClient(SHIM_URL) };
    } catch (err) {
      return { error: errorMessage(err) };
    }
  }, []);

  const masthead = (
    <Masthead>
      <MastheadMain>
        <MastheadBrand>
          <Title headingLevel="h1" size="lg" style={{ whiteSpace: "nowrap" }}>
            Konveyor Agentic Runs{" "}
            <span style={{ fontWeight: 400, opacity: 0.7 }}>(prototype)</span>
          </Title>
        </MastheadBrand>
      </MastheadMain>
      <MastheadContent>
        <span className="masthead-shim">shim: {SHIM_URL}</span>
      </MastheadContent>
    </Masthead>
  );

  const onList = view.kind === "list" || view.kind === "playbooks";

  return (
    <Page masthead={masthead}>
      {api.error || !api.client ? (
        <PageSection>
          <Alert variant="danger" isInline title="Invalid shim URL (VITE_SHIM_URL)">
            {api.error ?? "no client"}
          </Alert>
        </PageSection>
      ) : (
        <>
          {onList && (
            <PageSection style={{ paddingBottom: 0 }}>
              <ToggleGroup aria-label="Run kind">
                <ToggleGroupItem
                  text="Agent runs"
                  buttonId="nav-runs"
                  isSelected={view.kind === "list"}
                  onChange={() => setView({ kind: "list" })}
                />
                <ToggleGroupItem
                  text="Playbook runs"
                  buttonId="nav-playbooks"
                  isSelected={view.kind === "playbooks"}
                  onChange={() => setView({ kind: "playbooks" })}
                />
              </ToggleGroup>
            </PageSection>
          )}
          {view.kind === "list" ? (
            <RunsPage
              api={api.client}
              onOpenRun={(runName) => setView({ kind: "detail", runName })}
              onOpenPlaybookRun={(name) => setView({ kind: "playbookDetail", name })}
            />
          ) : view.kind === "playbooks" ? (
            <PlaybookRunsPage
              api={api.client}
              onOpenPlaybookRun={(name) => setView({ kind: "playbookDetail", name })}
            />
          ) : view.kind === "playbookDetail" ? (
            <PlaybookRunDetailPage
              api={api.client}
              name={view.name}
              onBack={() => setView({ kind: "playbooks" })}
              onOpenRun={(runName) =>
                setView({ kind: "detail", runName, fromPlaybookRun: view.name })
              }
            />
          ) : (
            <RunDetailPage
              api={api.client}
              runName={view.runName}
              onBack={() =>
                setView(
                  view.fromPlaybookRun
                    ? { kind: "playbookDetail", name: view.fromPlaybookRun }
                    : { kind: "list" },
                )
              }
            />
          )}
        </>
      )}
    </Page>
  );
}
