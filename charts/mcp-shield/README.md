# mcp-shield Helm chart

Deploys [mcp-shield](https://github.com/EricMarcantonio/mcp-shield), a Zero
Trust gateway for the Model Context Protocol, to Kubernetes.

Read this whole file before installing into anything but a scratch
namespace -- the approval API/dashboard has no authentication, and this
chart's defaults are built around that fact.

## TL;DR

```sh
helm install mcp-shield oci://ghcr.io/ericmarcantonio/charts/mcp-shield \
  --version 0.1.0 \
  --namespace mcp-shield --create-namespace
```

That gets you a single gateway pod with:

- A 1Gi PVC backing the SQLite approvals database, mounted at `/data`.
- No upstream MCP servers configured (`config.servers: []`) -- add your
  own before this is useful, see below.
- The client-facing proxy (`:8080`) reachable in-cluster at
  `<release>-mcp-shield-proxy:8080`.
- The approval API/dashboard (`:8081`) reachable **only** via
  `kubectl port-forward` -- see "Reaching the dashboard" below. It is not
  exposed by a NodePort, LoadBalancer, or Ingress by default.

## Before you install: read the security model

mcp-shield's approval API and dashboard, on port 8081, have **no
authentication of any kind**. Anyone who can send it an HTTP request can
list, approve, or reject pending capability changes for every upstream
server this gateway proxies -- see the project's
[`docs/security-model.md`](../../docs/security-model.md). This chart's
defaults treat that as the central constraint, not an afterthought:

- `dashboard.service.type` defaults to `ClusterIP` (no NodePort/
  LoadBalancer).
- `dashboard.ingress.enabled` defaults to `false`.
- `dashboard.networkPolicy.enabled` defaults to `true`, with an empty
  `allowFrom` -- which, because a NetworkPolicy that selects a pod for
  Ingress default-denies any port with no matching rule, makes `:8081`
  unreachable over the pod network from anywhere, including other pods in
  the same namespace.

Every one of those is a value you can change, but each one is documented
in `values.yaml` with exactly what you're opting into. Read the comment
before you flip it.

## Reaching the dashboard

The supported way to reach the approval dashboard is `kubectl
port-forward`, which attaches to the pod directly and bypasses
NetworkPolicy entirely (it doesn't go over the pod network):

```sh
kubectl --namespace mcp-shield port-forward \
  svc/mcp-shield-mcp-shield-dashboard 8081:8081
```

Then open <http://localhost:8081/> for the dashboard, or use the API
directly:

```sh
curl localhost:8081/api/manifests/pending
curl -X POST localhost:8081/api/manifests/1/approve \
  -d '{"username":"you","reason":"reviewed"}'
```

`helm install`/`helm upgrade` prints the exact port-forward command for
your release name in the post-install notes.

## Configuring upstream MCP servers

mcp-shield spawns each upstream MCP server as a subprocess over stdio, per
`config.servers` (mirrors `config/servers.example.json` in the repo root:
`name`/`command`/`args`/`env`). By default this is empty and the gateway
proxies nothing. Add servers via values:

```yaml
config:
  servers:
    - name: calendar
      command: /app/bin/some-upstream-server
      args: ["--flag"]
      env: ["API_KEY=changeme"]
```

**This renders into a Kubernetes Secret by default** (`config.type:
secret`), because a server's `env` array is mcp-shield's documented hook
for injecting upstream credentials. That said, `values.yaml` (and `helm
get values`) is not a secrets store -- for a real credential, populate a
Secret out-of-band (`kubectl create secret`, an external-secrets operator,
sealed-secrets) and point the chart at it instead of templating the
plaintext through Helm:

```yaml
config:
  existingSecret: my-mcp-shield-servers
```

The Secret/ConfigMap just needs a single key, `servers.json`, holding the
same JSON array `config/servers.json` would.

## Persistence and why replicas are pinned to 1

mcp-shield's entire state -- the approvals audit trail: who authorized
which capability change -- lives in one SQLite file. SQLite does not
support safe concurrent writers across multiple processes sharing that
file. Because of that:

- `replicaCount` is enforced at render time to be exactly `1`. Setting it
  to anything else makes `helm template`/`helm install` fail outright with
  an explanatory error, rather than silently deploying something that
  will eventually corrupt the audit trail.
- The Deployment's update strategy is hardcoded to `Recreate`, not
  `RollingUpdate` (and is not a `values.yaml` knob) -- `RollingUpdate`
  briefly runs the old and new pod side by side, which either fails to
  schedule the new pod (normal on an RWO volume) or, on infrastructure
  that allows multi-attach, lets two processes open the same database
  file at once.
- `persistence.accessMode` defaults to `ReadWriteOnce`, which is what
  makes most CSI drivers refuse to attach the volume to more than one
  node at a time -- a second independent guard against concurrent writers.
  Don't change it to `ReadWriteMany`.

Set `persistence.enabled: true` (the default) for anything but a
disposable demo; `false` uses an `emptyDir`, so the audit trail -- and
every server's approval state -- is lost on every pod restart or
reschedule.

If you need multiple replicas for throughput or availability, that isn't
this chart: swap `database.Store` for a Postgres-backed implementation
(an interface the mcp-shield codebase already exposes for this) and build
a different deployment around it.

## Security context

The container image is distroless (no shell, no package manager). This
chart runs it as:

- Non-root, fixed UID/GID 65532 (`runAsNonRoot: true`, matching the
  `nonroot` convention used across `gcr.io/distroless/*` images).
- `readOnlyRootFilesystem: true` -- the binary and dashboard assets are
  baked into the image and never written to at runtime; the only writable
  path is the SQLite database, on the PVC.
- All Linux capabilities dropped, `allowPrivilegeEscalation: false`, and
  the `RuntimeDefault` seccomp profile.
- `fsGroup: 65532` on the pod, so Kubernetes chowns the PVC and the
  servers.json Secret/ConfigMap to a group the process can actually write
  as -- without this, a freshly provisioned PVC (typically owned
  `root:root`) leaves a non-root process unable to create the database
  file.

mcp-shield currently fails to start if the parent directory of
`DATABASE_PATH` doesn't exist -- a known bug being fixed separately in the
application itself. This chart doesn't depend on that fix: the database
path (`database.path`, default `/data/mcp.db`) always lives directly under
the PVC's mount point (`persistence.mountPath`, default `/data`), and
Kubernetes creates that directory as part of mounting the volume, before
the container's entrypoint ever runs.

## Probes

Both liveness and readiness point at `GET :8081/healthz` -- the only HTTP
health endpoint mcp-shield exposes today. It's a shallow check (confirms
the API server is up, not that SQLite or any upstream server is healthy);
there's currently no deeper endpoint to point at. kubelet talks to the pod
IP directly for probes, not through the dashboard Service, so these
continue to work even with the default locked-down NetworkPolicy -- see
the caveat in `templates/networkpolicy.yaml` if your CNI is unusual.

## Values reference

See `values.yaml` directly -- every field has an inline comment explaining
what it does and what changing it costs you. The sections most worth
reading before your first install are `persistence`, `dashboard`, and
`config`.

## Verifying an install

```sh
helm lint charts/mcp-shield
helm template mcp-shield charts/mcp-shield | kubectl apply --dry-run=server -f -
helm install mcp-shield charts/mcp-shield --namespace mcp-shield --create-namespace
helm test mcp-shield --namespace mcp-shield --logs
```

`helm test` runs a small in-cluster check that curls `:8081/healthz` --
note that if you've tightened `dashboard.networkPolicy.allowFrom`, this
test pod is subject to the same policy a real unauthorized caller would
be, and is expected to fail unless it's covered by your allow-list too.
