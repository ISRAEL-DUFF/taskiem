# Contract: Idempotency keys

## Derivation

```
material = "taskiem/idem/v1" ‖ 0x00 ‖ tenant_id ‖ 0x00 ‖ seed ‖ 0x00 ‖ step_id ‖ 0x00 ‖ attempt_group (decimal)
seed     = value of effect.idempotency_seed if declared, else run_id
digest   = SHA-256(material)
```

- `tenant_id` and `run_id` are canonical lowercase UUID strings. `0x00` separators make the encoding unambiguous; a seed containing `0x00` is rejected.
- `step_id` is the step **instance** id that decide records, such as `pay` or, inside a `foreach`, `pay_all[3].pay_employee` (nested loops chain: `outer[1].inner[2].pay`). When the step declares `effect.idempotency_seed`, it is the plain step id instead (`pay_employee`), because the seed is expected to identify the item and the key should stay stable across runs and forks.
- Compensating actions use `compensate:<instance id>`.
- `attempt_group` starts at 0 and increments only when a reconcile returns `not_found` or a human explicitly re-issues the effect. Automatic retries keep it.
- Forked runs reuse the parent's `run_id` as the seed for steps copied from the parent, so a fork never re-pays a completed step.

## Encoding

The connector's `idempotency` block encodes `digest` for the provider:

| `encoding` | Alphabet | Bits per char |
| --- | --- | --- |
| `base32_lower` | `a-z2-7` (RFC 4648, lowercase, no padding) | 5 |
| `hex_lower` | `0-9a-f` | 4 |
| `base64url` | `A-Za-z0-9-_` (no padding) | 6 |

The first `length` characters of the encoded digest are used, after `prefix`. Registration requires at least 128 bits (`length × bits ≥ 128`) and checks `prefix` and the alphabet against `limits`.

Paystack: `prefix: tsk_`, `base32_lower`, `length: 32` → 36 characters, 160 bits, within 16–50 characters of `[a-z0-9_-]`.

## Recording

`EffectIntent` records the derivation inputs (tenant, seed, step id, attempt group) and the encoded key, so support staff can map a provider reference back to a run and step.

## Test vectors

`engine/effects/testdata/vectors.json` holds fixed inputs and expected keys; any implementation (Go engine, future SDKs, external auditors) must reproduce them.
