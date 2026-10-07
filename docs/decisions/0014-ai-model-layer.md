# 0014 — The AI model layer and the builder's safety boundary

Date: 2026-10-07 · Status: Accepted

## Context

Phase 3 (milestone B1) adds an AI workflow builder (spec 12). It must work with a hosted frontier model by default and with self-hosted open models for tenants with strict residency (12.3), and it must never be able to publish, approve, read secrets or write to a real provider (gate G3). Prompts carry tenant context, so personal data and secrets are the main data risks; the model's output is untrusted.

## Decision

1. **A small provider interface in `engine/ai`.** `Complete(ctx, Request) (*Response, error)` with system blocks (each optionally a cache breakpoint), messages, an optional JSON Schema for structured output, max tokens and effort; the response carries text, parsed JSON, usage, stop reason and the model that answered. Three implementations: Anthropic (default), an OpenAI-compatible client for self-hosted models, and a deterministic fake for tests and offline evaluation. Vendor SDK types stay inside the provider.
2. **Claude through the official Go SDK**, `claude-opus-5-5` by default, configurable per deployment. Thinking left to the model (Opus 5.5 cannot disable it) and depth set with `output_config.effort`; the stable system prompt and the connector catalogue are cached prefixes; requests above 16k max tokens stream; server-side refusal fallbacks (`fallbacks: "default"`) are on, and `stop_reason: refusal` is checked before content is read. The API key comes from `ANTHROPIC_API_KEY` only.
3. **Structured output constrains an envelope, not wd/v1.** The wd/v1 schema is recursive, uses `if`/`then` per step type and has open objects (step inputs), none of which strict structured outputs accept. The model returns `{summary, assumptions, workflow (a JSON string), tests}`, the full wd/v1 schema is in the system prompt, and the definition is validated afterwards by the publishing checks, with failures fed back for up to three corrections.
4. **Redaction is structural.** `ai.Redacted` applies the PII redactor to every system block and message; the builder wraps whatever provider it is given, so no call path skips it.
5. **The safety boundary is the import graph.** `engine/ai` and `engine/ai/builder` may not import the vault, the runtime, the database, the audit chain, ingest, the sandboxes, `wdcheck`, the API or any connector package; a test asserts it. The builder receives the publishing check as a function, connectors as manifests with their handlers stripped, and tenant context as plain structs that cannot hold a credential, secret or variable value. It runs with no principal. Saving a proposal is an API call by a person with `workflow.edit`, creates a `draft` version authored by that person, and records the build as co-author; publishing is unchanged.
6. **Budgets are a plan limit.** `ai_monthly_tokens` joins `tenant_limits` (platform default 2,000,000 tokens), counted from the insert-only `ai_interactions` log. Hitting it refuses AI building only.
7. **Retrieval is keyword scoring**, not embeddings: manifests are few and well named, and scoring is deterministic and explainable. Revisit when tenant connectors or workflows number in the thousands.

## Consequences

- Swapping or adding a provider touches one file; the pipeline, logs and tests are unchanged.
- The model can be wrong, injected or refused without consequence beyond a bad proposal: every proposal passes the checks a human's work does and is reviewed by a person.
- Recording each interaction before acting on it means a database outage stops AI building (not runs).
- A JSON-in-a-string definition costs some output tokens in escaping and loses token-level schema enforcement for the definition itself; correction rounds make up for it. If structured outputs gain recursive schemas, the envelope's `workflow` can become a typed object.
- Token budgets do not price cache reads lower than fresh input; a currency budget can replace them once plans are priced (AI3).
