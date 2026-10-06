# Git integration

Each environment can be connected to one repository on GitHub (github.com or Enterprise) or GitLab (gitlab.com or self-managed), in one of two modes (spec 10.3). Set it up under **Secrets & settings → Git**, or with `PUT /v1/git/{env}`; it needs the `git.manage` permission (owners and admins).

| Mode | Source of truth | What happens |
| --- | --- | --- |
| **Platform-led** | Taskiem | Publishing a version also opens a pull or merge request (branch `taskiem/<id>-v<n>`) with the definition (`flows/<name>.wd.json`) and its generated code (`.flow.ts`), so the repository keeps a reviewed history. Publishing does not wait for the merge. A request that could not be opened is reported with the publish and can be retried (`POST /v1/workflows/{wf}/versions/{v}/git-request`) |
| **Git-led** | The repository | A push to the connected branch deploys. Workflows the repository holds are read-only in Taskiem: saving or publishing them is refused with the file to change |

## Repository layout

```
flows/            workflow definitions (*.wd.json) and their code (*.flow.ts)
tests/            workflow tests (*.test.json); tests under flows/ are run too
```

Both directories are configurable. Definitions are what deploy; keep each `.flow.ts` and its `.wd.json` in step with `taskiem build` and check it in CI with `taskiem build --check` and `taskiem test` (see the [CLI](cli.md)).

## A Git-led deploy

1. The host sends a push webhook to `/git-hooks/{tenant}/{env}`, served by the API and by the edge role. Taskiem checks the signature (GitHub's `X-Hub-Signature-256` HMAC, or GitLab's `X-Gitlab-Token`), ignores other branches and events, and queues a sync. The host gets its answer at once.
2. The API role works the queue. It reads every `*.wd.json` and `*.test.json` under the two directories at the pushed commit.
3. It checks every definition as publishing would, and runs every workflow test. **If any check or test fails, nothing is deployed** (spec 10.5); the sync is marked failed with the problems and failing cases.
4. Otherwise, in one transaction, each definition is matched to a workflow by its `id`. A new one is created; a changed one gets a new version recording the commit; each is published. Every publish is in the audit log with the commit, repository and file.
5. The sync and its report are listed in settings and at `GET /v1/git/{env}/syncs`. "Deploy from <branch> now" (`POST /v1/git/{env}/sync`) deploys the branch head by hand.

A workflow removed from the repository is left as it is: Taskiem never deletes on a sync.

## Credentials

- **Token**: a GitHub fine-grained or classic personal access token, or a GitLab project or personal access token. Git-led mode needs read access to repository contents. Platform-led mode also needs write access to contents and to pull or merge requests.
- **GitHub App**: an installation of your own GitHub App, with `auth: {type: "github_app", app_id, installation_id, private_key}` through the API. Installation tokens are fetched and cached by Taskiem.

Credentials and the webhook secret are stored as encrypted secrets of the environment (`git_credentials`, `git_webhook_secret`). Connecting checks the branch can be read before anything is saved. The webhook secret is shown once, when it is created or rotated (`rotate_webhook_secret: true`). Calls to the Git host go through the egress guard, so a self-hosted URL cannot reach private or metadata addresses.

## Not yet

- **Per-environment deploys.** Publishing in Taskiem is not yet per environment (staging support is Phase 2, milestone 5), so a Git-led sync publishes for every environment, as the web app does.
- **A Taskiem GitHub App.** Today each tenant brings a token or its own GitHub App. A published Taskiem App, installed with one click, needs the organisation to register it.
- **Bitbucket.**
