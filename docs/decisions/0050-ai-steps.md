# 0050. AI steps: asking a model at run time, with personal data masked

**Status:** Accepted (2026-10-24)

## Context

wd/v1 has had an `ai` step since Phase 1: a prompt, an output schema and an optional model. It validated and failed at run time as unsupported, the last step type to do so. The pieces it needs exist: the provider layer (`engine/ai`, Anthropic and self-hosted), structured output, the monthly token budget, and the interaction log the builder and repair use. What was undecided was the data question: a run carries personal data, sealed in history, and a step that sends it to a model sends it to a third party.

Other products mostly send whatever the author maps into the prompt. Zapier and n8n leave redaction to a step the author adds; Workato's masking hides job history, not what reaches a model; Microsoft's controls are admin switches for the whole environment. Salesforce's Einstein Trust Layer masks by default, finding personal data by pattern and by field classification, sending placeholders, and putting the values back in the answer. Taskiem knows more than the others: every personal value a run holds is sealed and recorded as such (`x-pii`, the detectors, taint).

## Decision

1. **Masked by default.** Before a call, the run's personal data in the prompt and the input (every value the run holds sealed, whole or inside a text, and what the detectors recognise) is replaced by placeholders such as `[[phone_1]]`; one value, one placeholder. The model is told what they are; the placeholders in its answer are put back, and the output carrying the values is sealed in history. A prompt or input text holding personal data is sealed whole in history too.
2. **Let through by field, audited.** A step lists in `personal_data` the input fields the model must read as they are. The list is checked against the step's input when the version is saved, and publishing a version with such steps writes `ai.personal_data.allow` to the audit log, naming the steps and fields.
3. **An owner's switch, and the provider's terms shown.** `governance_settings.ai_steps` (on unless an owner turns it off): off, saving a workflow with an ai step is refused and a published one fails when the step starts. The plan's `ai` feature gates AI steps as it gates building. The deployment states what its provider keeps (`TASKIEM_AI_RETENTION`), shown with the model on the Governance page and in `GET /v1/ai/status`.
4. **The answer is typed.** The output is the answer, which must match `output_schema` (an object schema; objects are closed when sent as structured output). A mismatch is sent back once; a second fails the step (`invalid_output`). A refusal fails (`refused`), a cut-off answer fails (`invalid_output`), and neither is retried; a rate limit, provider error or timeout is retried under the step's policy. Asking is a read.
5. **Counted and recorded.** Each call is an `ai_interactions` row keyed by run and step, with personal data masked, counting against the monthly AI budget (with a plan's AI overage, as for building); over budget the step fails (`limit`). Steps run on a new `ai` worker queue, on by default.
6. **Models.** A step may name a model the deployment offers (`TASKIEM_AI_STEP_MODELS`), checked when the version is saved; otherwise the default model answers.

## Alternatives considered

| Alternative | Why not |
|---|---|
| Send as written, as most automation tools do | A run's personal data would reach a third party whenever an author mapped it, with nothing in the workflow saying so |
| Redact with masks (`[phone]`) as the builder does | The answer could not carry the values back; an ai step that drafts a reply to a customer needs the customer's name in it |
| A separate redaction step the author adds | Easy to forget, and it duplicates what the run already knows about its data |
| Refuse ai steps any personal data at all | Some tasks need it (reading a free-text address); an audited, per-field allowance is the narrower door |

## Consequences

- `engine/pii` gains `Masker` (placeholders out, values back, what was put back reported for sealing) and `Taint.Within`.
- The worker gains `AISteps` and the `ai` queue; `TASKIEM_WORKER_QUEUES` includes `ai` by default.
- `ai_interactions` gains `run_id` and `step_id`; `governance_settings` gains `ai_steps` (migration 00218).
- The builder proposes an ai step only when the business may use one (rule 21, context `ai_steps`).
- Every wd/v1 step type now runs.

## Provenance

From a discussion on 2026-10-24: the founder asked what it would take to implement the ai step, then how other products handle personal data reaching a model, and asked for all three controls: masked by default, let through by field with an audit, and an owner's switch with the provider's terms shown.
