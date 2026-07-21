# In-cluster deployment (interim, pre-Hub)

Runs the browser UI + gateway **on the cluster** — no laptop processes.
Both components are interim by design: the gateway seat is replaced by the
real Konveyor Hub passthrough proxy later, and the UI page is absorbed
into tackle2-ui (see `docs/adr/0004-client-contract-and-transports.md`).

```
browser ── ingress/route (TLS + SSO) ── agentic-ui (nginx, static SPA)
                                            │  /api + WS, same-origin
                                        agentic-gateway (SA + RBAC)
                                            │  CRs via k8s API · pod :4000 via service DNS
                                        agentic-controller / Agent Sandbox / sandbox pods
```

Differences from laptop mode: the gateway **direct-dials**
`<sandboxName>.<ns>.svc:4000` (no port-forwarding; auto-detected
in-cluster, `ACP_DIAL` overrides) and authenticates as a namespace-scoped
ServiceAccount instead of your kubeconfig.

## Build the images (context = repo root)

```sh
# local cluster (minikube):
minikube image build -t agentic-gateway:dev -f deploy/gateway/Dockerfile .
minikube image build -t agentic-ui:dev      -f deploy/ui/Dockerfile .

# real cluster: docker build + push, then kustomize-patch the image refs.
```

## Deploy

```sh
kubectl apply -k deploy/manifests
kubectl -n konveyor-agents rollout status deploy/agentic-gateway deploy/agentic-ui
```

Prereqs already on the cluster: the agentic-controller (deployed from
the repo root: `make deploy`), Agent Sandbox, and the sample
Agents/LLMProviders (`hack/demo-up.sh` converges all of that).

## Access

With an ingress (see `ingress.example.yaml`) — or without one:

```sh
kubectl -n konveyor-agents port-forward svc/agentic-ui 8081:80
open http://localhost:8081
```

Everything (REST + WebSocket chat) flows through that single origin.

## Verify

The browser-constraint smoke works through the deployed stack end to end
(nginx → gateway → controller → sandbox ACP):

```sh
cd packages/hub-shim
SHIM_URL=http://localhost:8081 npx tsx dev/browser-smoke.ts
```

## Teardown

```sh
kubectl delete -k deploy/manifests
```

## Auth (deliberate gap)

The gateway has no auth of its own — protect it at the ingress (oauth2-proxy,
or an OpenShift Route with an oauth-proxy sidecar). Per-user identity and
RBAC on runs is precisely the value the real Hub proxy adds; bolting a full
auth stack onto this interim component would be building Hub twice.
