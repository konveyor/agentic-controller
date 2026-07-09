# agentic-gateway: the hub-shim packaged for in-cluster use.
# Build context is the REPO ROOT (the shim imports ../agentrun-client/src):
#   docker build -t agentic-gateway:dev -f deploy/gateway/Dockerfile .
#   minikube image build -t agentic-gateway:dev -f deploy/gateway/Dockerfile .
FROM node:22-slim

WORKDIR /app

# Install each package's deps (tsx runs the TypeScript sources directly,
# matching the repo's dev style; dev deps are required for that).
COPY packages/agentrun-client/package*.json packages/agentrun-client/
COPY packages/hub-shim/package*.json packages/hub-shim/
RUN cd packages/agentrun-client && npm ci --no-audit --no-fund \
 && cd ../hub-shim && npm ci --no-audit --no-fund

COPY packages/agentrun-client packages/agentrun-client
COPY packages/hub-shim packages/hub-shim

# In-cluster defaults: bind all interfaces; ACP dial mode auto-detects
# "direct" from the serviceaccount env (no port-forwarding in-cluster).
ENV HOST=0.0.0.0 \
    PORT=7080 \
    NAMESPACE=konveyor-agents

USER node
EXPOSE 7080
WORKDIR /app/packages/hub-shim
CMD ["npx", "tsx", "src/server.ts"]
