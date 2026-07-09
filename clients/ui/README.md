# Konveyor Agentic Runs UI (prototype)

Vite + React 18 + PatternFly 6 front-end over the hub-shim HTTP/WS API. It
consumes `@konveyor/agentic-client` straight from source via a Vite alias —
no build step for the core package.

Prerequisites: the agentic-controller live on the cluster, and the hub-shim
running locally (`cd packages/hub-shim && npm run dev`), default
`http://127.0.0.1:7080` — override with `VITE_SHIM_URL`.

    cd ui && npm install
    npm run dev     # opens on http://localhost:5173; `npm run build` to verify
