# The `taskiem` CLI

One binary runs the engine and is the command-line tool (spec 10.4). `taskiem help` lists every command.

## Offline

| Command | What it does |
| --- | --- |
| `taskiem validate FILE...` | Checks workflow code and definitions as publishing would: the `wd/v1` contract, connectors and actions that exist in this binary, code that compiles, triggers it can serve. Also checks connector manifests (`*.yaml`) |
| `taskiem build [--check] [PATH...]` | Compiles workflow code (`*.flow.ts`, [SDK](sdk.md)) under the paths (default `flows/`) and writes the definition beside it (`*.wd.json`) when it changed. `--check` writes nothing and fails if a definition is out of date |
| `taskiem codegen [--write] FILE...` | Prints definitions as workflow code, or with `--write` writes `*.flow.ts` beside each |
| `taskiem test [-run RE] [-v] [PATH...]` | Runs workflow tests (`*.test.json`, [format](contracts/wd-test-v1.md)) under the paths (default `.`). Exits non-zero if a case fails, printing why and the run's events |

## Against a server

These use `$TASKIEM_URL` (default `http://localhost:8080`) and an API key in `$TASKIEM_API_KEY`, or `--url` and `--key`. Create a key in the web app under API keys with the permissions the command needs (`workflow.read`, `workflow.edit`, `workflow.publish`, `run.read`).

| Command | What it does |
| --- | --- |
| `taskiem diff [PATH...]` | Compares local `*.wd.json` files (default `flows/`) with the server, matched by the definition's `id`: new workflows, unchanged ones, and for changed ones which steps were added, removed or changed, against the published version |
| `taskiem deploy [--dry-run] [PATH...]` | Saves each changed workflow as a new version and publishes it. Publishing runs the server's checks; a version that fails them stays a draft and the problems are printed |
| `taskiem promote [--from staging] [--to prod] [PATH...]` | For each local workflow, runs in `--to` the version it runs in `--from`. `--to` must be gated on `--from` (see [Environments](environments.md)); needs `workflow.publish` |
| `taskiem runs tail [--workflow ID] [RUN_ID]` | Prints run events as they are recorded: one run until it ends, or every active run (of one workflow, by its id or `wf_...` key) until interrupted |

Deploy publishes to every environment not gated on another; `promote` carries a version into a gated one.

## Connectors and the catalogue

`taskiem connector init|build|validate|test|check|push` write, test and upload your own WebAssembly connectors; `keygen|package|verify|publisher|submit|submissions|publish|withdraw|revoke` take one through the public catalogue ([connector SDK](connector-sdk.md), [connector submissions](connector-submissions.md)). Operators review submissions, verify publishers and revoke versions with `taskiem catalogue reviewers|publishers|queue|show|review|revoke` (database access; audited). `taskiem connector` and `taskiem catalogue` without arguments print their usage.

## Local development

`taskiem dev` runs every engine role in one process with the web app (when `web/dist` is built) and watches a directory:

```sh
taskiem dev --flows flows
```

- **Database.** A private Postgres cluster in `.taskiem/dev/pg`, created with the Postgres 16 installed on the machine (`initdb` and `pg_ctl` on `PATH` or in the usual Debian and Homebrew locations), on a free loopback port, stopped on exit. Or pass `--dsn` (a schema owner) to use any database. Postgres is required because the engine needs `SKIP LOCKED`, row-level security, `LISTEN/NOTIFY` and partitioning.
- **First start.** Migrates, creates a tenant and a developer account (`dev@taskiem.local`; the password is in `.taskiem/dev/state.json`), and an API key for the CLI, which it prints.
- **Hot reload.** Whenever a `*.flow.ts`, `*.wd.json` or `*.test.json` changes, changed code is built to its definition, all workflow tests run, then every changed workflow whose tests pass is deployed. A workflow with a failing test keeps its running version.

`.taskiem/` holds keys and data; it is git-ignored.
