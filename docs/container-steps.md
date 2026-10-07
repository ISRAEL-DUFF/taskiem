# Container steps

A `container` step runs a program from a container image: for work that needs native libraries or more than a [code step](code-steps.md) allows, such as rendering PDFs, processing images or scoring with a machine-learning model (spec 7.5). Each attempt runs in a fresh sandbox, on a separate pool of workers, under gVisor, with no network unless the step asks for it.

Container steps are enabled per plan: a tenant needs container minutes (`container_minutes_monthly`) before its steps run, and the platform needs a container runner (see [Operator setup](#operator-setup)).

```json
{"id": "render", "type": "container",
 "input": {"html": "=steps.invoice.output.html", "key": "=secrets.render_key"},
 "config": {
   "image": "registry.example.com/tools/pdf-render@sha256:4f1c…e9",
   "command": ["/usr/local/bin/render"],
   "args": ["--format", "a4"],
   "secrets": ["render_key"],
   "class": "read",
   "limits": {"cpu": "1", "memory_mb": 1024, "timeout": "5m"}
 }}
```

In code (`@taskiem/sdk`):

```ts
.step("render", container(
  { image: "registry.example.com/tools/pdf-render@sha256:4f1c…e9", command: ["/usr/local/bin/render"], class: "read" },
  { html: ({ steps }) => steps.invoice.output.html },
))
```

## The program

| | |
| --- | --- |
| **Input** | The step's `input`, evaluated, as one JSON document: on stdin (`input_mode: "stdin"`, the default), or in the file named by `$TASKIEM_INPUT` (`input_mode: "file"`). |
| **Output** | One JSON value: everything the program writes to stdout (`output_mode: "stdout"`, the default), or the file named by `$TASKIEM_OUTPUT` (`output_mode: "file"`; stdout then goes to the logs). Nothing at all is `null`. It becomes `steps.<id>.output`. |
| **Logs** | stderr (and stdout in file mode), kept with the step's result up to 16 KB. Do not log personal data. |
| **Exit status** | 0 for success. Anything else fails the step with the status and the end of the logs. |
| **Environment** | `TASKIEM_RUN_ID`, `TASKIEM_STEP_ID`, `TASKIEM_ATTEMPT`, `TASKIEM_INPUT`, `TASKIEM_OUTPUT`, `HOME=/tmp`; for writes `TASKIEM_IDEMPOTENCY_KEY`; the declared secrets; with network egress, `HTTPS_PROXY` and `HTTP_PROXY`. Nothing else from the platform. |
| **File system** | Read-only, except `/tmp` (256 MiB) and the output directory. |
| **User** | 65532, never root, with no capabilities. |

`command` is required: the image's own entrypoint is not used. The program runs under a small supervisor (`taskiem-shim`) the platform copies into the sandbox; it feeds the input, collects the output within its cap and reports the result.

## Images

`image` must name its registry and be pinned by digest: `registry.example.com/team/tool@sha256:<64 hex digits>`. Tags (`:latest`, `:1.2`) are refused when the definition is validated, because a tag can be moved to other code after the workflow was reviewed. Docker Hub images are written in full: `docker.io/library/python@sha256:…`.

The operator allows registries (`TASKIEM_CONTAINER_REGISTRIES`, for example `registry.example.com/steps`); an image from anywhere else fails the step before anything starts. To find an image's digest: `docker buildx imagetools inspect registry.example.com/tools/pdf-render:1.4` or `crane digest …`.

## Secrets

`secrets` lists the vault secrets the step may read, by name. Each is read once per attempt (and recorded in the secret-read log, purpose `step.container`) and given to the program as an environment variable of the same name (`secrets_mode: "env"`, the default) or as a file of that name in the directory `$TASKIEM_SECRETS` (`secrets_mode: "file"`). Secrets can also be passed in the input (`"key": "=secrets.render_key"`), as in other steps.

Secret values never appear in the sandbox's Pod specification; they travel in a per-attempt Kubernetes Secret deleted with the Pod. Anything the step returns, logs or fails with is scrubbed of every secret value it was given before it is recorded.

## Limits

| | Default | Maximum |
| --- | --- | --- |
| `limits.cpu` | `"1"` (one core) | `"4"`; at least `"100m"` |
| `limits.memory_mb` | 512 | 4096 |
| `limits.timeout` | `5m` | `30m` |
| `limits.output_bytes` | 262144 (256 KB) | 1048576 (1 MB) |
| Logs | 16 KB | — |

A definition asking for more than a maximum is refused when it is validated; a definition stored before a maximum was lowered is held to the new maximum when it runs. CPU and memory are guaranteed, not shared: a step gets what it asks for and no more, and a program using more memory than its limit is killed.

The plan sets two more limits (operators: `taskiem tenants limits`):

- `container_minutes_monthly`: container time per UTC month, counted from when the program started to when it ended, as the sandbox (not the program) reports it, rounded up to the second. **0 means container steps are off**, and 0 is the platform default (`TASKIEM_DEFAULT_CONTAINER_MINUTES_MONTHLY`). Once the month's minutes are used up, container steps fail until next month; a step already running finishes. Usage is shown in `GET /v1/limits` (`container_seconds_this_month`).
- `container_concurrency`: the tenant's container steps running at once (default 2, `TASKIEM_DEFAULT_CONTAINER_CONCURRENCY`); more wait in the queue.

A partner's sub-tenants inherit both and can only be given less; a partner without container minutes cannot give them to its sub-tenants.

## Failures and retries

A container step is an `unsafe_write` unless its `class` says otherwise, because the platform cannot know what an arbitrary program did. Declare `read` for a step without side effects (a renderer, a calculation) and `idempotent_write` for one that passes `TASKIEM_IDEMPOTENCY_KEY` on to whatever it changes, so retrying it is safe. `effect.idempotency_seed` works as for other steps.

| What happened | Kind | `read`, `idempotent_write` | `unsafe_write` |
| --- | --- | --- | --- |
| The program never started: the image could not be pulled, the Pod was not scheduled in time | `not_sent` | retried | retried |
| It exited with a non-zero status, its output was too large or not JSON | `fatal` | fails | fails |
| It timed out, ran out of memory, its node was lost, or the run was cancelled while it ran | `unknown_outcome` | retried | parks the run for a person |
| Container steps off for the plan, minutes used up, image not allowed | `fatal` | fails | fails |

Retries follow the step's `retry` (default: 3 attempts, exponential backoff). Every attempt gets a fresh sandbox; nothing carries over.

Cancelling a run, or losing a `parallel` race, stops a running container step within a couple of seconds: its Pod is deleted. So does shutting down a container worker for longer than its 30-second drain (a rolling update of the pool): steps it was running end as `unknown_outcome`, so prefer `read` and `idempotent_write` steps, and roll the pool when it is quiet.

## Networking

`network: "none"` (the default): the program has no network at all. There is no route out, no DNS, and no proxy address.

`network: "egress"` with `hosts`: the program may make HTTP and HTTPS requests through the platform's egress proxy, given to it as `HTTPS_PROXY` and `HTTP_PROXY` (most HTTP clients use them without changes). The proxy allows a host only if it is on **both** the step's `hosts` and the environment's allow-list (Secrets & settings → Allowed hosts), only on ports 443 and 80, never to private, loopback, link-local or cloud-metadata addresses, and logs every connection. `*.example.com` matches subdomains, not `example.com` itself. The proxy's credentials are valid only while the step runs.

```json
"config": {"image": "…", "command": ["/app/sync"], "class": "idempotent_write",
           "network": "egress", "hosts": ["api.example.com"]}
```

## Security model

The container sandbox is trust boundary B15 in the [threat model](security/threat-model.md). In production (the Kubernetes runner) each attempt is a Pod in a dedicated namespace:

- **Runtime**: a gVisor RuntimeClass (`runsc`): the program's system calls are served by a user-space kernel, not the node's.
- **Pod**: non-root (65532), read-only root file system, every capability dropped, no privilege escalation, the runtime's default seccomp profile; no service account token, no service links, no DNS resolver; `restartPolicy: Never`; guaranteed CPU and memory; `activeDeadlineSeconds` as a backstop to the step's timeout. The namespace enforces the `restricted` Pod Security Standard and has a ResourceQuota.
- **Network**: a NetworkPolicy allows no traffic in and none out except to the container workers' egress proxy. The worker refuses to start if the policy is missing.
- **Data**: the input, declared secrets and the proxy token are in a Secret owned by the Pod; the Pod and the Secret are deleted when the attempt ends, whatever happened, and a sweep removes any a dead worker left behind.
- **Images**: pinned by digest, from allowed registries only.
- **Access**: only the container worker pool has Kubernetes API credentials, through a Role limited to Pods, their logs and Secrets in the sandbox namespace.

The program runs under `taskiem-shim`, which is not a security control: it runs as the program's user, so a program can tamper with its own result, which it controls anyway. Usage is measured from the kubelet's timestamps, not from anything inside the sandbox.

## Operator setup

1. **A gVisor node pool and RuntimeClass.** On GKE, create a node pool with GKE Sandbox enabled; it provides the `gvisor` RuntimeClass. Elsewhere, install gVisor's `runsc` and its containerd shim on a dedicated pool, taint the nodes, and create the RuntimeClass (the chart can: `containerSteps.runtimeClass.create`, with `handler: runsc` and the pool's node selector and tolerations under `scheduling`). Firecracker-based runtimes (Kata Containers) work the same way: set `containerSteps.runtimeClassName`.
2. **A registry.** A private registry, or a path of one, holding the images tenants may use; list it in `containerSteps.registries`. Put a pull secret in the sandbox namespace and name it in `containerSteps.imagePullSecrets` if the registry needs one.
3. **A CNI that enforces NetworkPolicy** (Calico, Cilium, GKE Dataplane V2).
4. **Enable the chart's container steps** ([Kubernetes](kubernetes.md#container-steps)):

   ```yaml
   containerSteps:
     enabled: true
     registries: [registry.example.com/steps]
   ```

   This adds the sandbox namespace (Pod Security `restricted`), its NetworkPolicy and ResourceQuota, a service account with a Role in that namespace only, and the container worker Deployment (`TASKIEM_WORKER_QUEUES=container`, the egress proxy on port 3128).
5. **Give tenants minutes:** `taskiem tenants limits <tenant> --set container_minutes_monthly=600`.

The worker reads these settings (the chart sets them):

| Variable | |
| --- | --- |
| `TASKIEM_CONTAINER_RUNNER` | `kubernetes`, or `local` for development |
| `TASKIEM_CONTAINER_NAMESPACE` | the sandbox namespace |
| `TASKIEM_CONTAINER_RUNTIME_CLASS` | default `gvisor` |
| `TASKIEM_CONTAINER_REGISTRIES` | allowed image prefixes, comma-separated |
| `TASKIEM_CONTAINER_SHIM_IMAGE` | an image holding `/taskiem-shim` (the Taskiem image does) |
| `TASKIEM_CONTAINER_IMAGE_PULL_SECRETS` | pull secrets in the sandbox namespace |
| `TASKIEM_CONTAINER_NODE_SELECTOR` | `key=value,…` for sandbox Pods |
| `TASKIEM_CONTAINER_NETWORK_POLICY` | the policy that must exist; default `taskiem-sandbox` |
| `TASKIEM_CONTAINER_PROXY_LISTEN` | the egress proxy's listener; default `:3128` |
| `TASKIEM_CONTAINER_PROXY_ADDR` | where sandboxes reach it: the worker Pod's IP and port |

### Development

`TASKIEM_CONTAINER_RUNNER=local` runs container steps as plain processes on the worker's host: the image is ignored and `command` runs from the host's `PATH` in a temporary directory, with the step's timeout, rlimits on memory, CPU time and file size, an environment holding only what the step is given, and (on Linux, where unprivileged user namespaces are allowed) an empty network namespace for network `none`. **It is not a sandbox**: the program runs as the worker's user and sees its file system. It refuses to start unless `TASKIEM_CONTAINER_LOCAL_DEV=1` is set as well. Never use it where people you do not trust can publish workflows.
