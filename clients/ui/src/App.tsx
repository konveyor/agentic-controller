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
} from "@patternfly/react-core";
import { ShimClient } from "@konveyor/agentic-client/transport-shim";
import { errorMessage } from "./format";
import { RunsPage } from "./components/RunsPage";
import { RunDetailPage } from "./components/RunDetailPage";

// Dev default: the local shim. Production (static build behind nginx):
// same-origin — nginx proxies /api (HTTP + WebSocket) to the gateway.
const SHIM_URL =
  import.meta.env.VITE_SHIM_URL ??
  (import.meta.env.DEV ? "http://127.0.0.1:7080" : window.location.origin);

type View = { kind: "list" } | { kind: "detail"; runName: string };

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

  return (
    <Page masthead={masthead}>
      {api.error || !api.client ? (
        <PageSection>
          <Alert variant="danger" isInline title="Invalid shim URL (VITE_SHIM_URL)">
            {api.error ?? "no client"}
          </Alert>
        </PageSection>
      ) : view.kind === "list" ? (
        <RunsPage
          api={api.client}
          onOpenRun={(runName) => setView({ kind: "detail", runName })}
        />
      ) : (
        <RunDetailPage
          api={api.client}
          runName={view.runName}
          onBack={() => setView({ kind: "list" })}
        />
      )}
    </Page>
  );
}
