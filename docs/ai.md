# Building workflows with AI

Taskiem can draft a workflow from a plain-language goal (spec 12.1). The model proposes and people dispose: the AI's output is only ever a proposal, checked like a person's work, and nothing it drafts runs until a person has saved it, published it and, where policy requires, had it approved.

## What it does

From the workflow list (**Build with AI**) or the editor (**Change with AI**), a person with `workflow.edit` describes what the workflow should do. The server then runs the builder pipeline (`engine/ai/builder`):

1. **Context.** It ranks every connector available to the tenant against the goal by keyword overlap (names, categories, actions, triggers, with a small synonym table; no vector database) and takes the full schemas of the top six, plus a one-line catalogue of all of them. It adds the tenant's connections by name and connector, variables by name, active approval policies by name and a summary of who approves, and the structure of up to three similar workflows (step ids, types and connector actions, never their inputs). When the goal changes an existing workflow, that workflow's latest definition is included.
2. **Draft.** The model answers with structured output (see [the schema choice](#structured-output)): a summary, assumptions to confirm, the definition, and up to three test cases.
3. **Validate.** The definition goes through exactly the checks publishing runs (`wdcheck`: the wd/v1 schema and semantic rules, connector and action existence, code compilation, trigger checks), plus the builder's policy rule: a write action of a `payments` connector that no approval step precedes is flagged (`payment_write_without_approval`).
4. **Self-correct.** Problems and policy findings are sent back to the model, up to three times. Whatever remains is shown: problems as "would not publish yet", policy findings as warnings.
5. **Dry run.** The draft runs in `engine/wdtest` (the real orchestrator on a virtual clock, every connector, HTTP and code step mocked): a generated happy path, with every step's output sampled from its action's output schema and every approval approved, and the cases the model proposed.
6. **Review.** The panel shows the proposal on a read-only canvas with the summary, assumptions, problems, warnings, dry-run results, rounds and tokens. **Save as draft** creates a new workflow, or a new version of the one being changed, in state `draft`.

Builds run in the background (`POST /v1/ai/build` answers 202 with an id; `GET /v1/ai/builds/{id}` reports the stage and, at the end, the proposal), because drafting and three corrections can take minutes.

| Route | Permission | What it does |
| --- | --- | --- |
| `GET /v1/ai/status` | `workflow.read` | Whether AI building is on, the model, this month's budget and use |
| `POST /v1/ai/build` | `workflow.edit` | `{goal, workflow?, environment?}`; starts a build. 503 `ai_disabled` when no provider is configured, 429 `ai_budget_exhausted` over budget, 429 `rate_limited` beyond five builds at once per tenant (one more every 20 seconds) |
| `GET /v1/ai/builds/{id}` | `workflow.edit` | Status (`running`, `proposed`, `failed`, `saved`), stage, proposal |
| `POST /v1/ai/builds/{id}/save` | `workflow.edit` | `{name?}`; saves the proposal as a draft version, once |
| `GET /v1/ai/builds/{id}/interactions` | `audit.read` | Every model call of the build, as logged |

## Safety model

The rule (spec 12, gate G3): **the AI can never publish, approve, read secrets, or execute a write against a real provider.** It is enforced by structure, not by prompting:

- **No path to the vault, the database, approvals or workers.** `engine/ai` and `engine/ai/builder` hold no database handle, vault, principal or connector handler. `engine/ai/imports_test.go` fails the build if their import graph ever reaches `engine/secrets`, `engine/runtime` (workers, publishing state, approval decisions), `engine/db`, `engine/audit`, `engine/ingest`, `engine/wasmconn`, `engine/sandbox`, `engine/wdcheck` (which pulls in the runtime; the API injects the check as a function), `api` or any `connectors/` package.
- **Manifests only.** The builder wraps the tenant's connector lookup in `ManifestOnly`, which strips every handler: the dry run sees schemas and action classes, and there is nothing it could call.
- **No AI principal.** A build runs with no principal at all. The context it gets is read by the API with queries that select names and summaries only (`connections.name, connector, status`; `variables.name`; policy documents for the dry run and a who-approves summary for the prompt), never `secrets`, `connections.secret_ref` or variable values. A test seeds a secret, a connection credential and a variable value and asserts none reaches a prompt.
- **Saving is a person's act.** Saving needs `workflow.edit` and creates a `draft` version with `created_by` set to the person; the version records the build (`workflow_versions.ai_build_id`) as its AI co-author. Publishing goes through the usual route, permission (`workflow.publish`) and four-eyes rules, where the person who saved counts as the author. `TestAIRoutes` pins the five routes under `/v1/ai`; adding one fails the test until it is reviewed against this section.
- **Prompt injection.** Connector descriptions, a tenant's workflow names and the goal itself are data in the prompt; the instructions say so. Injection can at worst change the proposal, and a proposal is validated like human work and reviewed by a person before anything is saved, let alone published.

## Data handling

- **Redaction.** Every prompt passes through `ai.Redacted`, which applies the PII redactor (`pii.Redact`: emails, Nigerian phone numbers, BVNs, NINs, card numbers) to every system block and message before it leaves the process. The builder wraps its provider itself, so no caller can skip it. The stored goal is redacted too.
- **What is logged.** Every model call is a row in `ai_interactions` (forced RLS, insert-only for the application): the build, round, kind (draft or correction), who asked, provider, the model that answered, the SHA-256 of the stable system prompt, the messages as sent (redacted), the answer (redacted again), stop reason, outcome (`valid`, `problems`, `unparseable`, `refused`, `truncated`, `error`) and token usage. Each build is a row in `ai_builds` with the proposal. Each proposal appends `ai.propose` to the audit chain (rounds, validity, tokens, policy violations, `co_author: ai:<model>`), and each save appends `ai.save` (and `workflow.create` for a new workflow).
- **Where prompts go.** To the configured provider only. With Anthropic, that is Anthropic's API under the deployment's account and its data-processing terms; tenants with strict residency need a self-hosted model ([needs people](needs-people.md#phase-3), AI1).

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `ANTHROPIC_API_KEY` | — | Turns AI building on with Claude. Read from the environment (a Kubernetes secret) only; never stored or logged |
| `TASKIEM_AI_PROVIDER` | `anthropic` when the key is set, otherwise off | `anthropic`, `selfhosted`, or `off` |
| `TASKIEM_AI_MODEL` | `claude-opus-5-5` | Required for `selfhosted` |
| `TASKIEM_AI_BASE_URL` | — | `selfhosted`: an OpenAI-compatible endpoint (`http://llm.internal:8000/v1`); `anthropic`: optional gateway |
| `TASKIEM_AI_API_KEY` | — | `selfhosted` bearer token, if the server wants one |
| `TASKIEM_AI_EFFORT` | `high` | Thinking depth for drafting: `low`, `medium`, `high`, `xhigh`, `max` |
| `TASKIEM_AI_MAX_TOKENS` | 32000 | Per answer, thinking included (1024–128000); above 16000 requests stream |
| `TASKIEM_AI_FALLBACKS` | on | `off` stops sending server-side refusal fallbacks |
| `TASKIEM_DEFAULT_AI_MONTHLY_TOKENS` | 2,000,000 | Platform default budget ([plan limits](operations.md#plan-limits)) |

### Claude

The Anthropic provider (`engine/ai/anthropic.go`) uses the official Go SDK (`github.com/anthropics/anthropic-sdk-go`, MIT) on the Messages API's beta surface, because server-side fallbacks are a beta parameter:

- **Model** `claude-opus-5-5` by default. Thinking is left unset: Opus 5.5 always thinks adaptively and cannot turn it off; depth is set with `output_config.effort` (`high` for drafting).
- **Structured output** through `output_config.format` (see below).
- **Prompt caching.** The system prompt is two blocks, each a cache breakpoint: the instructions with the full wd/v1 JSON Schema (identical for every tenant), then the connector catalogue (identical for tenants with the same connectors, ordered by ref with actions sorted, so the bytes never vary). Everything per request (goal, context, the definition being changed) comes after, in the user message. Every round of a build, and every build of a tenant within the cache's lifetime, reads that prefix from the cache; the `cache_creation_tokens` and `cache_read_tokens` logged per call show whether it does.
- **Streaming** when `max_tokens` is above 16,000, accumulated into one message.
- **Refusals.** Requests carry `fallbacks: "default"` with the `server-side-fallback-2026-07-01` beta, so a request a safety classifier declines is re-served by Anthropic's recommended fallback model within the same call. The provider checks `stop_reason` before reading content: on `refusal` partial output is discarded; the build fails with "the model declined this request" if it was the first draft, or keeps the last draft with a warning if it was a correction.

### Structured output

The draft is constrained to an **envelope**, not to wd/v1 itself: `{summary, assumptions[], workflow: string, tests: [{name, case: string}]}` with `additionalProperties: false`. The wd/v1 schema cannot be a structured-output schema: steps are recursive, step types are selected with `if`/`then`, many strings carry patterns, and a step's `input` is an object with arbitrary keys, which strict schemas (every object closed) cannot express. So the definition travels as a JSON string, the full wd/v1 schema is in the system prompt for the model to write to, and the definition is validated afterwards by the same code as a human's. A string that is not JSON, or a definition that fails validation, is fed back as a problem like any other.

### Self-hosted open models

`TASKIEM_AI_PROVIDER=selfhosted` speaks the OpenAI-compatible chat completions API that vLLM, llama.cpp's server, Ollama and TGI expose, with `response_format: json_schema` for the envelope (servers that ignore it still work: the answer is validated either way). Prompt caching, effort and refusal fallbacks are Anthropic features and are not sent. Which open model to offer is a decision for people (AI1); nothing in the pipeline depends on the provider.

## Budgets

Each tenant has a monthly AI budget in tokens, `ai_monthly_tokens` in its [plan limits](operations.md#plan-limits): the platform default (2,000,000, or `TASKIEM_DEFAULT_AI_MONTHLY_TOKENS`) unless an operator sets another (`taskiem tenants limits <tenant> --set ai_monthly_tokens=5000000`; 0 means no limit). Every token a call processes counts (input, output, cache writes and cache reads), summed from `ai_interactions` for the UTC month; tenants see their use in `GET /v1/limits` and in the panel.

The budget is checked before each model call. A tenant over budget gets 429 `ai_budget_exhausted` with "build the workflow on the canvas instead"; a build that runs out between corrections returns its last draft with a warning. Only AI building stops: runs, triggers and manual editing are never affected. The amounts per plan are a business decision (AI3).

## Evaluation

`tools/aieval` runs the builder over a suite of requests (spec 12.4) and reports the rates gate G3 measures:

```sh
go run ./tools/aieval                                   # offline, with a keyword heuristic as the "model"
go run ./tools/aieval -provider anthropic -out r.json   # Claude, with ANTHROPIC_API_KEY
go run ./tools/aieval -tags payments -min-first-try 0.7 # fail below 70% valid and test-passing on the first try
```

Suites are JSON lines in `evals/builder/*.jsonl`: `{"id", "request", "expect": {"connectors": [...], "steps": [...]}, "tags": [...]}`. The report gives, per case and overall: valid on the first try, valid after correction, dry-run tests passing, policy violations, rounds, tokens, and recall of the expected connectors and step types. The 25 seed requests come from the dogfood flows and the connector catalogue; the 200+ real requests B3 needs come from people (AI2). The offline heuristic exercises the pipeline and sets a floor; it is not a measure of any model.

## What is not built yet

- Repair of failed runs through a shadow sandbox (B2), the 200-request suite and its gate in CI (B3), and building from WhatsApp (B4).
- Cost budgets in currency (tokens only for now), and per-plan amounts (AI3).
- An `ai` step type that calls a model at run time: it validates but fails with kind `unsupported`.
