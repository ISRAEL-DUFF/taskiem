# Environments and promotion

A tenant starts with `dev` and `prod`. Each environment has its own secrets, connections, variables, allowed hosts and triggers (webhooks name theirs with `?env=`; schedules fire in `prod` only), and **runs its own version of each workflow**.

## Staging

Add an environment under **Secrets & settings → Environments** (`POST /v1/environments`, `workflow.publish`) and choose how it takes versions:

- **When published** (ungated): publishing a version deploys it here, with its triggers. A new ungated environment starts with every workflow's published version.
- **By promotion from another environment** (gated): only a version running in the named environment can reach it. Gating `prod` on `staging` gives the usual path: publish → `dev` and `staging` → test there → promote to `prod`.

```http
POST /v1/environments            {"name": "staging"}
PUT  /v1/environments/prod       {"promotion_from": "staging"}
POST /v1/workflows/{id}/promote  {"from": "staging", "to": "prod"}
```

The workflow page shows the version each environment runs, with **Promote** where an environment is behind the one it is gated on. From the command line: `taskiem promote --from staging --to prod flows/`.

## Rules

- Promotion copies the version that runs in the source environment; it never edits it, and a different version cannot be named (`409`). The definition and its policies are checked again.
- A gated environment runs only the version promoted to it: a run there cannot pin another version. A newer publish leaves it alone, so what runs in `prod` changes only by promotion.
- With **four-eyes publishing** on, promoting needs a person who neither wrote the version nor deployed it to the source environment; API keys cannot promote then. Every promotion is audited (`workflow.promote`, with both environments).
- A key limited to one environment can promote only into it.
- An environment connected to Git in Git-led mode deploys from its branch, not by promotion; Git review is its gate. Gates cannot form a circle.
- Changing a gate changes nothing that is running; it applies to the next publish or promotion.
- Adding a gate needs `workflow.publish`; lifting a gate or pointing it at another environment, which lets publishes reach the environment directly, needs an owner. Keys limited to one environment can do neither.
- Outside `dev`, a run of an ungated environment can pin a version other than the deployed one only for someone with `workflow.publish`.
