import { createRoot } from "react-dom/client";
import "@patternfly/react-core/dist/styles/base.css";
import "./app.css";
import { App } from "./App";

// No StrictMode on purpose: its dev-mode double-mount would open and tear
// down a second ACP WebSocket/port-forward tunnel per view, which muddies
// manual testing of the chat flow against a real sandbox.
createRoot(document.getElementById("root")!).render(<App />);
