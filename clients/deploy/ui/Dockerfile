# agentic-ui: static build of the SPA served by unprivileged nginx, which
# also proxies /api (HTTP + WebSocket) to the agentic-gateway Service so
# the browser talks same-origin.
# Build context is the REPO ROOT (the ui imports ../packages/agentic-client/src):
#   docker build -t agentic-ui:dev -f deploy/ui/Dockerfile .
#   minikube image build -t agentic-ui:dev -f deploy/ui/Dockerfile .
FROM node:22-slim AS build
WORKDIR /src
COPY packages/agentic-client packages/agentic-client
COPY ui ui
# VITE_SHIM_URL deliberately unset: the production bundle falls back to
# window.location.origin (same-origin; nginx owns the /api proxying).
RUN cd ui && npm ci --no-audit --no-fund && npm run build

FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY deploy/ui/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/ui/dist /usr/share/nginx/html
EXPOSE 8080
