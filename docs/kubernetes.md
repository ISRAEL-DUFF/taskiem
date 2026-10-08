# Running Taskiem on Kubernetes

Two ways to install, from one source:

- **Helm chart**, `deploy/helm/taskiem`. Use this one.
- **Plain manifests**, `deploy/kubernetes/taskiem.yaml`, rendered from the chart with its defaults and the ingress turned on (`make chart`). CI fails if the file is stale. Use it with `kubectl apply` or as a Kustomize base when Helm is not an option.

Both need Kubernetes 1.27 or later, a Postgres 16 database, and (in production) OpenBao for key management.

## What gets installed

`mode: split`, the default, runs one Deployment per role. Each role is the same image started as `taskiem serve --role <role>`:

| Role | Default replicas | Serves | Probes |
|---|---|---|---|
| `api` | 2 | Web app and HTTP API on 8080 | `/readyz` (readiness) and `/healthz` (liveness) on 8080 |
| `edge` | 2 | Webhooks (`/hooks/`), Git push hooks (`/git-hooks/`) and the platform WhatsApp number (`/channels/whatsapp`) on 8081 | `/readyz` (readiness) and `/healthz` (liveness) on 8081 |
| `orchestrator` | 2 | Nothing; advances runs | `/healthz` on 9090 |
| `scheduler` | 1 | Nothing; runs schedules, timers, retention archiving, audit anchoring and alerts | `/healthz` on 9090 |
| `worker` | 2, or autoscaled | Nothing; runs connector and sandbox steps for the shared pool | `/healthz` on 9090 |
| `worker-<pool>` | per entry of `workerPools` (none by default) | The same, for tenants routed to that pool ([below](#dedicated-worker-pools)) | `/healthz` on 9090 |

Every role also serves Prometheus metrics on 9090. `mode: all` runs every role in a single Deployment instead, which suits small installs and trials.

Alongside the Deployments the chart creates:

- a Service per role;
- an Ingress, if enabled, that sends `/hooks/`, `/git-hooks/` and `/channels/` to the edge role and everything else to the API;
- a migration Job (a Helm pre-install and pre-upgrade hook);
- a PersistentVolumeClaim for the scheduler;
- PodDisruptionBudgets;
- optionally: a worker HorizontalPodAutoscaler, NetworkPolicies, a Prometheus Operator ServiceMonitor, a PrometheusRule with the SLO recording rules and burn-rate alerts (`prometheusRule.enabled`; point `prometheusRule.runbookBaseURL` at your copy of [reliability.md](reliability.md)) and the SLO dashboard as a ConfigMap for Grafana's sidecar (`grafanaDashboard.enabled`).

## Install

1. **Create the database and the Secret.** The chart never holds secrets. Put them in a Secret whose keys are environment variables:

   ```sh
   kubectl create namespace taskiem
   kubectl -n taskiem create secret generic taskiem \
     --from-literal=TASKIEM_DATABASE_URL='postgres://taskiem@db.internal:5432/taskiem?sslmode=verify-full' \
     --from-file=TASKIEM_OPENBAO_TOKEN=./openbao-token \
     --from-file=TASKIEM_ANCHOR_KEY=./anchor-key
   ```

   Add these keys only if you use them:

   - `TASKIEM_LOCAL_KMS_KEY`, only with `TASKIEM_KMS=local`, which is not for production;
   - `TASKIEM_SMTP_URL`, for email alerts.

   The database user must own the schema, because migrations run as it. Pods switch to `taskiem_app` on every connection, and row-level security applies to that role (`TASKIEM_DATABASE_ROLE`; see [operations.md](operations.md)).

2. **Write your values.** At minimum:

   ```yaml
   image:
     tag: "0.1.0"
   publicURL: https://taskiem.example.com
   config:
     TASKIEM_OPENBAO_ADDR: https://openbao.internal:8200
     TASKIEM_ALERT_FROM: taskiem@example.com
   ingress:
     enabled: true
     className: nginx
     host: taskiem.example.com
     annotations:
       cert-manager.io/cluster-issuer: letsencrypt
   ```

3. **Install:**

   ```sh
   helm install taskiem deploy/helm/taskiem -n taskiem -f values.yaml
   ```

   The migration Job runs first. If it fails, the install stops and the old pods keep running. The schema only moves forward, and each release works with the schema of the release before it, so upgrades follow the same path: `helm upgrade` with the new tag.

4. **Create the first tenant (once).** Add a `TASKIEM_BOOTSTRAP_PASSWORD` key (12 or more characters) to the Secret and restart the API pods. Then run:

   ```sh
   kubectl -n taskiem exec deploy/taskiem-api -- taskiem bootstrap --tenant Acme --email you@example.com
   ```

   Remove the key afterwards. The image is distroless, so `exec` can only run `taskiem`; there is no shell.

With the plain manifests the steps are the same, except that you run `taskiem migrate` yourself first. The Job's hook annotations mean nothing to `kubectl apply`, so the Job runs as soon as it is applied, and Kubernetes will not update the finished Job in place. Delete it before applying a new version.

**Partners' custom domains.** A partner can serve the embedded builder on its own host name once it has verified the domain through the partner API ([embedding](embedding.md#10-custom-domains)). Each such host needs a certificate at the ingress: list it under `ingress.embedHosts` (with `ingress.embedAnnotations` naming your cert-manager issuer, or a `secretName` holding a certificate you manage). The chart adds a `<release>-embed` Ingress that sends only `/embed/` and `/v1/embed/` on those hosts to the API; the application issues no certificates.

## Security defaults

- **Pods:**
  - run as uid 65532, the distroless `nonroot` user;
  - use a read-only root filesystem;
  - drop every capability;
  - use the `RuntimeDefault` seccomp profile;
  - mount no service-account token (Taskiem never calls the Kubernetes API, except the container worker pool when container steps are enabled; see below).
- **Writable storage** is limited to two `emptyDir` volumes, `/tmp` and `/cache`. `/cache` holds compiled WebAssembly for the Python interpreter and connectors, and is rebuilt on start.
- **`TASKIEM_TRUST_PROXY` defaults to true** because traffic arrives through the ingress controller. Turn it off if pods are reachable any other way, since otherwise clients could forge `X-Forwarded-For`.
- **`networkPolicy.enabled`:**
  - Inbound traffic is limited to the API and edge ports, plus metrics from `networkPolicy.monitoringNamespace`.
  - Outbound traffic is not restricted, because connectors call many providers. The worker's egress guard already blocks private and metadata addresses; add your own egress policy if you need an allow-list.

## Container steps

Container steps ([container-steps.md](container-steps.md), spec 7.5) are off by default. They need a node pool running gVisor (`runsc`) with a RuntimeClass for it, a registry (or registry path) for the images tenants may use, and a CNI that enforces NetworkPolicy. Then:

```yaml
containerSteps:
  enabled: true
  registries: [registry.example.com/steps]
  # runtimeClassName: gvisor                 # GKE Sandbox provides "gvisor"
  # runtimeClass: {create: true, handler: runsc, nodeSelector: {pool: gvisor}, tolerations: [...]}
  # imagePullSecrets: [steps-registry]       # Secrets in the sandbox namespace
```

This adds:

| Object | Where | What |
| --- | --- | --- |
| Namespace `taskiem-sandbox` (`containerSteps.namespace`) | cluster | Pod Security `restricted` enforced; only sandbox Pods run here (`createNamespace: false` to bring your own) |
| NetworkPolicy `taskiem-sandbox` | sandbox namespace | no ingress; egress only to the container workers' proxy port (3128). The worker refuses to start without it |
| ResourceQuota `taskiem-sandbox` | sandbox namespace | `containerSteps.quota`: Pods, CPU and memory for the namespace as a whole |
| ServiceAccount `taskiem-container-worker` | release namespace | the only account that mounts a token |
| Role and RoleBinding | sandbox namespace | create, get, list and delete Pods; read Pod logs; create and delete Secrets; read NetworkPolicies. Nothing else, nowhere else |
| Deployment `taskiem-container-worker` | release namespace | `serve --role worker` with `TASKIEM_WORKER_QUEUES=container` and the Kubernetes runner settings; the egress proxy on 3128, advertised to sandboxes as the Pod's IP |
| NetworkPolicy `taskiem-container-worker` | release namespace | with `networkPolicy.enabled`: the proxy port only from the sandbox namespace, metrics from monitoring |
| RuntimeClass | cluster | only with `containerSteps.runtimeClass.create` |

The main worker Deployment does not serve the `container` queue; container steps wait for the container pool. Each container step is a Pod (`tsk-…`) that lives for the attempt; `kubectl -n taskiem-sandbox get pods -l taskiem.dev/sandbox=true` shows those running. Give tenants minutes with `taskiem tenants limits <tenant> --set container_minutes_monthly=<n>`.

## Dedicated worker pools

`roles.worker` is the shared pool. Each entry of `workerPools` adds a pool of its own for tenants an operator routes there (decision 0024, [cloud](cloud.md#dedicated-worker-pools)):

```yaml
workerPools:
  - name: acme
    queues: connector,sandbox
    replicas: 2
    nodeSelector: { pool: dedicated }   # optional: isolate it on its own nodes
    autoscaling: { enabled: true, minReplicas: 2, maxReplicas: 6 }
```

Each pool gets a Deployment `taskiem-worker-<name>` (`serve --role worker` with `TASKIEM_WORKER_POOL=<name>`), a metrics Service, an HPA with `autoscaling.enabled`, a PodDisruptionBudget when it keeps more than one replica, and a NetworkPolicy with `networkPolicy.enabled`. Pods are labelled `app.kubernetes.io/component: worker-pool` and `taskiem.io/worker-pool: <name>`. Unset fields take `roles.worker`'s resources and the chart's `nodeSelector` and `tolerations`. The chart refuses invalid or repeated names, `shared`, and the `container` queue. Split mode only.

Install the pool first, then route tenants with `taskiem pools assign <name> --tenant <id>` (or `--plan <plan>`): routing to a pool with no live worker is refused. Scale a pool on its backlog with KEDA on `taskiem_pool_ready{pool="<name>"}`.

## Storage

The scheduler mounts the claim at `/var/lib/taskiem`:

- `archive/` holds runs past retention.
- `anchors/` holds signed audit-chain heads.

Anchors are only worth something as a copy outside the database. Use a storage class with write-once (object-lock) semantics where you can, and back the volume up.

The claim is `ReadWriteOnce` and is kept when the release is uninstalled (`helm.sh/resource-policy: keep`). The scheduler (or `all`) Deployment uses the `Recreate` strategy so that two pods never mount it at once. Set `storage.existingClaim` to bring your own.

If `storage.enabled` is false:

- runs past retention are kept rather than purged;
- audit heads are not anchored outside the database;
- the scheduler logs a warning about both.

## Dedicated single-tenant deployments

An enterprise customer can have a deployment of its own: the same chart, installed once per customer, with its own namespace (or cluster), database and Secret. Nothing in the chart is specific to this. These settings make it single-tenant and put the customer in control of its keys:

- **One tenant.** Leave `TASKIEM_ALLOW_SIGNUP` off (the default), and create the customer's tenant with `taskiem bootstrap` (step 4 above).
- **The customer's KMS as the platform key.** Point `TASKIEM_KMS=openbao` and `TASKIEM_OPENBAO_ADDR` at the customer's OpenBao or Vault. Put a token scoped to the transit key `TASKIEM_KMS_KEY` in the Secret as `TASKIEM_OPENBAO_TOKEN`. Every tenant key in the deployment is then wrapped by the customer's key, and revoking that token or disabling the key stops the whole deployment from reading secrets. Rotating it, and what happens when it is unavailable, work as described in [BYOK](byok.md). Per-tenant BYOK still works on top.
- **A KMS on the customer's private network.** For per-tenant BYOK with a key service that has only a private address, set `config.TASKIEM_BYOK_ALLOW_PRIVATE: "on"`. The platform KMS (`TASKIEM_OPENBAO_ADDR`) is called directly, not through the egress guard, so it does not need this. Never set it on a multi-tenant deployment: it lets tenants' key addresses reach private ranges.
- **Revocation bound.** `config.TASKIEM_BYOK_CACHE_TTL` (default `5m`) bounds how long a revoked key keeps working in running pods. Set it on every role, which `config` does.
- **Network.** The chart's NetworkPolicies do not restrict egress. If you add an egress allow-list, include the customer's KMS address.

Infrastructure for dedicated deployments (where they run, who operates them, backups) is outside this chart. See [needs people](needs-people.md#phase-4).

## Shutdown and readiness

On SIGTERM a pod answers `/readyz` with 503 at once and keeps serving for `shutdown.delaySeconds` (10), so the Service and the ingress stop sending it requests before it stops listening. Then servers finish open requests and workers finish in-flight steps for up to `shutdown.workerDrainSeconds` (30), and release the leases of any they had to cut off, so other pods pick those steps up at once. `shutdown.gracePeriodSeconds` (60) becomes the pods' `terminationGracePeriodSeconds`; the chart refuses values that do not leave 10 seconds after the delay and the drain. Details: [reliability](reliability.md#graceful-shutdown).

## Sizing

The defaults are per-role `databasePool` and `resources`. Each pod opens up to `databasePool` connections, plus 2 for loading tenant connectors. Size Postgres `max_connections` to cover (pool + 2) × replicas, summed across roles and worker pools, with headroom for the migration Job. The defaults need about 170.

Workers need the most memory. Each running Python step can use up to 256 MiB of interpreter memory, which is why the default limit is 2 GiB. Scale workers on CPU with `roles.worker.autoscaling`, or on queue backlog (`taskiem_queue_ready`, `taskiem_queue_oldest_ready_seconds`) with KEDA.

## Values

[`values.yaml`](../deploy/helm/taskiem/values.yaml) documents every value. `config` becomes a ConfigMap of environment variables, and empty strings are left out. Every variable is described in [operations.md](operations.md). `extraEnv` adds raw `env` entries to every pod, for example `valueFrom` references to other Secrets.
