# Git integration

Each environment can be connected to one repository on GitHub (github.com or Enterprise), GitLab (gitlab.com or self-managed) or Bitbucket Cloud (bitbucket.org), in one of two modes (spec 10.3). Set it up under **Secrets & settings → Git**, or with `PUT /v1/git/{env}`; it needs the `git.manage` permission (owners and admins), and an API key not limited to one environment.

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

1. The host sends a push webhook to `/git-hooks/{tenant}/{env}`, served by the API and by the edge role. Taskiem checks the signature (GitHub's `X-Hub-Signature-256` HMAC, GitLab's `X-Gitlab-Token`, or Bitbucket's `X-Hub-Signature` HMAC) before it touches the repository credentials; an unknown tenant or environment gets the same `401` as a bad signature. It ignores other branches and events, and **pushes whose commit is no longer the branch head** (logged, `202`), so an old delivery sent again cannot roll the environment back. Each delivery is taken once, by its id (`X-GitHub-Delivery`, `X-Gitlab-Event-UUID`, or Bitbucket's `X-Request-UUID`). Then it queues a sync, and the host gets its answer.
2. The API role works the queue. It reads every `*.wd.json` and `*.test.json` under the two directories at the pushed commit.
3. It checks every definition as publishing would, and runs every workflow test. **If any check or test fails, nothing is deployed** (spec 10.5); the sync is marked failed with the problems and failing cases.
4. Otherwise, in one transaction, each definition is matched to a workflow by its `id`. A new one is created; a changed one gets a new version recording the commit; each is published. Every publish is in the audit log with the commit, repository and file.
5. The sync and its report are listed in settings and at `GET /v1/git/{env}/syncs`. "Deploy from <branch> now" (`POST /v1/git/{env}/sync`) deploys the branch head by hand.

A workflow removed from the repository is left as it is: Taskiem never deletes on a sync.

## Second person for Git-led connections

A Git-led connection deploys and activates approval policies on its own, with Git review as the second pair of eyes. So when the tenant has four-eyes on (publishing or policies) or the environment is gated, creating a Git-led connection, switching one to Git-led, or changing its provider, `api_url`, repository, branch or paths does not take effect at once: `PUT /v1/git/{env}` answers `202 pending_approval` and the change waits, listed under `requests` in `GET /v1/git`. Another person with `git.manage` (not whoever asked, and not an API key) approves it (`POST /v1/git/{env}/approve`) or rejects it (`POST /v1/git/{env}/reject`), with an optional `comment`. Until then the connection keeps its old settings and credentials; new credentials wait encrypted and are dropped on rejection. API keys cannot ask for such a change. Platform-led connections, new credentials for the same repository and webhook-secret rotation apply at once; a direct change withdraws a waiting one. Every request and decision is audited.

## Credentials

- **Token**: a GitHub fine-grained or classic personal access token, a GitLab project or personal access token, or a Bitbucket access token or API token ([below](#bitbucket-cloud)). Git-led mode needs read access to repository contents. Platform-led mode also needs write access to contents and to pull or merge requests.
- **GitHub App**: an installation of your own GitHub App, with `auth: {type: "github_app", app_id, installation_id, private_key}` through the API. Installation tokens are fetched and cached by Taskiem.

Credentials and the webhook secret are stored encrypted in the reserved `_git` environment (moved there from `git_credentials` and `git_webhook_secret` in the connection's environment by migration 00028): workflows cannot read them and the secrets API cannot list, change or delete them. Connecting checks the branch can be read before anything is saved. Stored credentials are reused only for the same provider, `api_url` and repository: changing any of those needs `auth` again, so they are never sent to a new host by someone who does not hold them. The webhook secret is shown once, when it is created or rotated (`rotate_webhook_secret: true`). Calls to the Git host go through the egress guard, so a self-hosted URL cannot reach private or metadata addresses.

## Bitbucket Cloud

The repository is `workspace/repo_slug`, as in `https://bitbucket.org/workspace/repo_slug`; leave the API URL empty (`https://api.bitbucket.org/2.0`).

**Credentials.** Either of:

- **An access token** (recommended): a repository access token (in the repository, **… → Settings → Security → Access tokens → Create access token**), or a project or workspace access token if one token should reach several repositories. It belongs to the repository, not to a person, and is sent as a Bearer token: `auth: {type: "token", token}`. The fewest scopes:
  - Git-led: **Repositories: Read** (`repository`).
  - Platform-led: **Pull requests: Write** (`pullrequest:write`), which includes reading and writing the repository, to make the request's branch and commit and open the pull request.
- **An API token** of a person's Atlassian account (**Account settings → Security → Create and manage API tokens → Create API token with scopes**, app **Bitbucket**), sent with Basic authentication: `auth: {type: "token", token, username: "<Atlassian account email>"}`; in Settings, fill in the account email. These scopes do not include one another: `read:repository:bitbucket` for Git-led, and for platform-led also `write:repository:bitbucket`, `read:pullrequest:bitbucket` and `write:pullrequest:bitbucket`. App passwords, which Bitbucket has deprecated, are sent the same way with the Bitbucket username.

**Webhook.** After connecting, add a webhook in the repository under **… → Settings → Workflow → Webhooks → Add webhook**: the URL shown by Taskiem, the **Secret** set to the webhook secret shown once (not a generated one), and the trigger **Repository push** (the default). Bitbucket then signs each delivery with HMAC-SHA256 in `X-Hub-Signature`; Taskiem refuses a delivery without a valid signature, ignores events other than `repo:push`, and ignores tags and deleted branches.

**Platform-led requests.** Bitbucket cannot move a branch, so a request branch left by an earlier attempt (`taskiem/<id>-v<n>`) is deleted and remade from the connected branch's head with the new files; an open pull request from it is reused, and its URL returned.

## Not yet

- **Per-environment deploys.** Publishing in Taskiem is not yet per environment (staging support is Phase 2, milestone 5), so a Git-led sync publishes for every environment, as the web app does.
- **A Taskiem GitHub App.** Today each tenant brings a token or its own GitHub App. A published Taskiem App, installed with one click, needs the organisation to register it.
