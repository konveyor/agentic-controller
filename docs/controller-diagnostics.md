# Controller memory diagnostics

The controller captures a memory baseline before cache synchronization and
logs periodic samples every 30 seconds after synchronization. Profiling is
disabled by default. These diagnostics support an OOM investigation; they do
not establish that a leak exists or that a particular memory limit fixes it.

Operator-managed HTTPS metrics and the Tackle CR controls below require the
separate operator integration, which is pending follow-up to this controller
change. That integration must add the role defaults, metrics reader identity,
and reconciliation of the Service, CA ConfigMap, token Secret, and ServiceMonitor
before applying the Deployment that requires the serving-certificate Secret.
Merge the controller change first to update the generated operator sync PR, then
add the integration above before merging that operator PR. Template synchronization
alone does not complete the operator integration.
Until that operator support ships, use the standalone controller flags; the
Tackle CR examples below describe the completed operator integration.

The default memory limit is 400Mi, with a 64Mi request. This raises the previous
128Mi ceiling to provide more headroom; it does not establish the OOM cause or
replace memory capture. Resource overrides remain available for investigation.

## Continuous capture

Controller-runtime exports process and Go runtime metrics, including
`go_memstats_heap_alloc_bytes`, `go_memstats_heap_objects`, `go_goroutines`, and
GC metrics, plus reconciliation and workqueue metrics. The operator creates an
`agentic-controller-metrics` Service and a ServiceMonitor when its CRD exists.
Scrapes use HTTPS and a dedicated reader identity that can only GET `/metrics`.
The monitor uses a Secret reference for credentials, rather than a filesystem
token path, so it works with OpenShift monitoring restrictions.

On OpenShift, service-serving certificates and an injected service CA configure
TLS verification. On another Kubernetes distribution, provide a TLS Secret named
`agentic-controller-metrics-tls` and the matching CA in ConfigMap
`agentic-controller-metrics-ca`, key `service-ca.crt`, to configure verified
scraping. The serving certificate must cover
`agentic-controller-metrics.<namespace>.svc`. Without these resources, the
non-OpenShift controller starts with a self-signed certificate and still logs
memory samples; the ServiceMonitor cannot verify its certificate yet.

OpenShift includes platform Prometheus monitoring by default. A Prometheus
instance must select this ServiceMonitor and its namespace for stored history.
User-workload monitoring is a separate, optional facility; `openshift-*`
namespaces are reserved for Red Hat components. This change does not relabel the
installation namespace or enable a cluster-wide monitoring stack. Check the
selected monitoring stack's target status and the query
`up{namespace="openshift-mta",service="agentic-controller-metrics"}`; a created
ServiceMonitor alone does not demonstrate successful capture.

Each `Controller memory sample` log entry includes `heap_alloc_bytes`,
`heap_live_bytes` (live heap after the most recent GC), `heap_goal_bytes`,
`heap_objects`, `runtime_total_bytes`, `heap_released_bytes`, `gc_cycles`, and
`goroutines`. Runtime memory is not the container's RSS or cgroup working set;
compare it with container memory and OOM/restart metrics from the cluster.
The sampler does not force GC or retain sample history in process. Log retention
across deleted pods depends on the cluster's log collection.

Tackle CR settings:

```yaml
spec:
  agentic_enabled: true
  agentic_memory_log_interval: "30s"  # 0 disables log samples
  agentic_pprof_bind_address: "0"    # profiling disabled
  agentic_metrics_enabled: true
```

For a standalone controller, the corresponding flags are
`--memory-log-interval=30s`, `--pprof-bind-address=0`, and
`--metrics-bind-address=:8443`. Secure metrics are enabled by default when a
metrics listener is configured. The Kustomize default overlay already enables
its HTTPS metrics Service; its optional Prometheus overlay requires scraper
credentials and CA configuration appropriate to the cluster.

## Capture an OOM and compare heap profiles

Before replacing a failing pod, inspect its `manager` container's
`lastState.terminated.reason`, restart count, previous logs, and node events.
`OOMKilled` establishes the termination reason; a restart count alone does not.
Capture workload size, active/completed run counts, and watched resource counts
with the memory history.

To enable profiling temporarily, set the Tackle CR field:

```yaml
spec:
  agentic_pprof_bind_address: "127.0.0.1:6060"
```

Wait for the Deployment rollout, then run in one terminal:

```bash
oc -n openshift-mta port-forward deployment/agentic-controller 6060:6060
```

In a second terminal, collect a baseline and another profile after repeating the
same workload and allowing it to become idle:

```bash
curl -fsS 'http://127.0.0.1:6060/debug/pprof/heap?gc=1' -o /tmp/agentic-baseline.pb.gz
# Repeat the workload, then capture again.
curl -fsS 'http://127.0.0.1:6060/debug/pprof/heap?gc=1' -o /tmp/agentic-after.pb.gz
go tool pprof -top -sample_index=inuse_space -base /tmp/agentic-baseline.pb.gz /tmp/agentic-after.pb.gz
curl -fsS 'http://127.0.0.1:6060/debug/pprof/goroutine?debug=1' -o /tmp/agentic-goroutines.txt
```

Use the actual installation namespace if it differs. Profiling accepts only a
loopback IP and port, and no Service exposes its port. Disable it again by
setting `agentic_pprof_bind_address: "0"`.

Look for increasing live heap after GC at comparable workload/object counts,
growing goroutine stacks, or queue/reconciliation activity that persists at idle.
A cache that grows with retained objects requires a different remedy from a leak
that grows after equivalent completed work. Take profiles before an OOM: a
SIGKILL cannot flush a final profile, and periodic samples can miss brief peaks.
