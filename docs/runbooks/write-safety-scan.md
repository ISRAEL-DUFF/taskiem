# Write-safety scan: code-step writes and shared loop seeds

Two engine changes refuse workflows that could move money wrongly. Neither touches a version that is already published: runs keep starting from it until its owner saves and publishes a new one. Run this scan on every environment before the release that carries them ships, and tell the owner of each workflow it lists.

| Change | What it refuses | What happens to a published version |
| --- | --- | --- |
| `host.fetch` sends only `GET` and `HEAD` ([code steps](../code-steps.md#the-host)) | A code step that sends a write. A worker that died after the call would run the script, and the write, again | The step's write now throws when it runs, so the step fails instead of sending. Move the write to an `http` step with a `class`, or a connector step |
| A seeded step inside a `foreach` must read `item` or `index` ([idempotency contract](../contracts/idempotency.md#derivation)) | A seed that is the same for every item, which gives every item one idempotency key | Still runs, and the provider still answers all but the first item as duplicates. Saving a new version shows the problem; publishing is refused until the seed reads the item |

## Running it

The queries are read-only. Run them as the admin role (`TASKIEM_DATABASE_URL`'s owner), which row-level security does not limit, so they see every tenant. They list only published and deprecated versions; drafts are checked when they are saved.

### Code steps that send writes

```sql
SELECT DISTINCT t.name AS tenant, w.name AS workflow, v.version, s->>'id' AS step,
       CASE WHEN s->'config'->>'source' ~* '["''`](post|put|patch|delete)["''`]' THEN 'sends a write'
            ELSE 'review: method is not a literal' END AS finding
FROM workflow_versions v
JOIN workflows w ON w.id = v.workflow_id
JOIN tenants t ON t.id = v.tenant_id
CROSS JOIN LATERAL jsonb_path_query(v.definition, 'strict $.**.steps[*] ? (@.type == "code")') AS s
WHERE v.state IN ('published', 'deprecated')
  AND s->'config'->>'source' ~ 'host\.fetch'
  AND s->'config'->>'source' ~* 'method|["''`](post|put|patch|delete)["''`]'
ORDER BY 1, 2, 3, 4;
```

`sends a write` is certain. `review` means the script passes a method it computes, which the query cannot read: open the step and check.

### Seeds shared by every item of a loop

```sql
SELECT DISTINCT t.name AS tenant, w.name AS workflow, v.version, s->>'id' AS step,
       s->'effect'->>'idempotency_seed' AS seed
FROM workflow_versions v
JOIN workflows w ON w.id = v.workflow_id
JOIN tenants t ON t.id = v.tenant_id
CROSS JOIN LATERAL jsonb_path_query(v.definition,
  'strict $.**.steps[*] ? (@.type == "foreach").config.**.steps[*] ? (exists(@.effect.idempotency_seed))') AS s
WHERE v.state IN ('published', 'deprecated')
  AND (s->'effect'->>'idempotency_seed') !~ '(^|[^A-Za-z0-9_.])(item|index)([^A-Za-z0-9_]|$)'
ORDER BY 1, 2, 3, 4;
```

Every row is a workflow whose loop has paid, or will pay, only its first item per run. Check the provider's records for the runs it has made: items after the first were answered as duplicates and never paid.

## Telling owners

For each row, write to the tenant's owners with the workflow, the step and what to change:

- **Code step write:** "Step *step* in *workflow* sends a *method* request from code. From *date*, code steps can only read, because a code step that is interrupted runs again from the start and would send the request twice. Move the request to an HTTP step with an action class, or to a connector step."
- **Shared seed:** "Step *step* in *workflow* uses the same idempotency seed, `seed`, for every item in its loop, so the provider treats every item after the first as a duplicate of it. Add the item to the seed, for example `=trigger.body.payroll_id + '/' + item.employee_id`, and publish a new version. Check the provider's records for items that were not paid."
