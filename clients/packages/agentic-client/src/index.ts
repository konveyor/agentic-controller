/**
 * @konveyor/agentic-client — isomorphic (browser + node) client core for
 * the konveyor agentic-controller.
 *
 * Subpath entry points (also usable directly):
 *   @konveyor/agentic-client/contract        CRD types + helpers
 *   @konveyor/agentic-client/acp             ACP session over WebSocket
 *   @konveyor/agentic-client/transport-shim  hub-shim HTTP transport
 */
export * from "./contract/index.js";
export * from "./acp/index.js";
export * from "./transport-shim/index.js";
