# USSD

A workflow can be offered on a USSD short code: a caller dials, walks a menu on their phone, confirms, and the workflow runs with what they entered. This is the fast path of the [architecture](spec/architecture.md#84-ussd-specifics) (section 8.4). The menu runs at the edge, inline, against cached state. Only the confirmation reaches the durable engine, and the caller hears the outcome by SMS.

Africa's Talking is the first aggregator. Other aggregators plug in through a small adapter (below).

## How it works

```
caller ──USSD──▶ operator ──▶ Africa's Talking ──POST form──▶ edge /channels/ussd/<tenant>/africastalking?token=…
                                                                │ 1. token (and address allow-list) checked
                                                                │ 2. session read or opened (ussd_sessions), pinned to a version
                                                                │ 3. menu walked from `text`, inline
                                                                ◀── "CON <screen>" or "END <message>"   (within 2 s)
                                         on confirmation:       │ 4. number and inputs sealed into the session, state confirmed
                                                                ◀── "END … Ref 4F2A9C1B"
                                                                └▶ background: StartRun (dedup on the session id)
                     notifier (api role) ── completed / failed ──▶ SMS through the tenant's Africa's Talking connection
```

1. **Every step is one callback.** Africa's Talking posts `sessionId`, `serviceCode`, `phoneNumber`, `networkCode` and `text`, where `text` is everything the caller has typed in this session joined with `*` (empty on the first request). The edge answers in plain text: `CON ` and a screen to wait for the next input, or `END ` and a message to close the session.
2. **Sessions are short.** Operators end a session after about 120 to 180 seconds, and after 15 to 60 seconds without an answer. Africa's Talking's help centre gives 180 seconds for Kenyan networks (Safaricom ends idle sessions after 30 seconds, Airtel after 60) and 120 seconds for Nigerian ones (30 seconds idle). Sessions are kept for 4 minutes.
3. **The answer never waits on the engine.** Each callback has a 2-second budget (`USSDSettings.Budget`). Walking the menu touches no database once the session is cached. A slow or stuck database costs the caller a "try again" message, never a hang. A confirmation does one short write and answers. The run is then started after the answer.
4. **Sessions work across edge replicas.** The session row in Postgres is the truth: tenant, provider, session id, the caller's hashed number, the workflow version, the reference and the state (`active`, `ended`, `confirmed`, `started`, `refused`). Each replica caches only what cannot change: the version a session is pinned to and that version's menu. Because Africa's Talking sends the whole path every time, any replica can walk any session.
5. **A confirmation starts one run.** The session moves from `active` to `confirmed` once, under a row lock. A retried or racing callback finds it confirmed and gets the same answer. `StartRun` is deduplicated on `africastalking:<session id>`, so two hand-offs (the edge and the notifier's backstop, or two replicas) start one run.
6. **Outcomes by SMS.** If the menu sets `notify.sms`, the notifier (api role, every 5 seconds) sends the `completed` or `failed` message when the run ends. It also sends the `failed` message when the run was refused (plan limit, workflow withdrawn, inputs that do not fit). Each message is claimed before it is sent and never sent twice: SMS has no idempotency key. The sealed number and inputs are then dropped from the session.

## Writing a menu

A menu is the `config` of a `ussd` trigger ([contract](contracts/wd-v1.md#ussd-menus)).

```json
"trigger": {
  "type": "ussd",
  "config": {
    "service_code": "*384*123#",
    "max_chars": 160,
    "screens": [
      { "id": "main", "type": "menu", "text": "Acme airtime", "input": "recipient", "options": [
          { "label": "Top up my number", "next": "amount", "value": "self" },
          { "label": "Top up another number", "next": "number", "value": "other" },
          { "label": "Help", "next": "help" } ] },
      { "id": "number", "type": "input", "text": "Enter the number", "input": "phone_number",
        "validate": { "pattern": "0[789][01][0-9]{8}" }, "error": "That is not a mobile number.", "next": "amount" },
      { "id": "amount", "type": "input", "text": "Amount in naira (50 to 5000)", "input": "amount",
        "validate": { "type": "integer", "min": 50, "max": 5000, "when": "=trigger.body.recipient == 'self' || trigger.body.amount <= 2000" },
        "next": "confirm" },
      { "id": "confirm", "type": "confirm", "text": "Send N{{amount}} airtime?", "confirm_label": "Send",
        "done": "Request received. Ref {{reference}}. We will send you an SMS." },
      { "id": "help", "type": "end", "text": "Help: 0800 000 0000." }
    ],
    "notify": { "sms": true, "completed": "Your top-up {{reference}} is done.", "failed": "Your top-up {{reference}} failed." }
  }
}
```

The whole example is `flows/examples/ussd-airtime-topup.wd.json`, with its code form beside it. In code, the trigger is `ussd({...})`, and conditions may be arrow functions: `when: ({ trigger }) => trigger.body.amount <= 2000`.

| Screen | Shows | Takes |
| --- | --- | --- |
| `menu` | `text`, then numbered `options` (up to 9) | A number. The option's `value` (or its label) is stored under the screen's `input`, if set. An option with `when` is shown only while its condition holds |
| `input` | `text` | Free input, checked against `validate` and stored under `input`: `type` (`text`, `number`, `integer`), `pattern` (RE2, whole input), `min`/`max` (numbers), `min_length`/`max_length`, and `when` (CEL over `trigger.body` with the new value in place). A refused input shows `error` (or a default) above the same screen |
| `confirm` | `text`, then `1. Yes` and `2. Cancel` (`confirm_label`, `cancel_label`) | `1` confirms: the inputs become the run's `trigger.body` and the caller sees `done`. `2` ends without starting anything |
| `end` | `text`, and the session ends | Nothing; nothing is started |

**Navigation.** On every screen except the first, `0` goes back one screen and forgets what that screen collected, and `00` returns to the start with nothing collected. On the first screen, `0` is ordinary input. An option list therefore never uses `0`.

**Placeholders.** Texts take `{{name}}` for an input collected so far, and `done` and the SMS texts also take `{{reference}}`. Values the inputs schema marks `x-pii`, and values that look personal (phone, account, card, BVN or NIN numbers), are masked as `****1234`. Texts are fixed: never expressions, so a screen can never show a secret, a variable or anything else from the platform.

**What the run gets.**

| Path | Value |
| --- | --- |
| `trigger.body` | The inputs collected, checked against the workflow's inputs schema before the run starts |
| `trigger.caller.phone_number` | The caller's number (`+234…`), sealed in history |
| `trigger.ussd` | `reference`, `service_code`, `provider`, `network` |

The run is started by `ussd:<reference>` in the environment the channel serves, with the version the caller walked, and is audited as `run.start` by the system actor `ussd:<reference>`.

### Checks at save and publish

`wd.Validate` checks every menu, so the editor, the CLI and publishing all report:

- **Screen size.** Each screen is measured at its worst case: the error line, the text with every placeholder at its longest, the options or confirm lines, and `0. Back`. Placeholder widths are the input's `max_length`, the longest option value, 15 characters for numbers, 20 for other text and 8 for masked values. The limit is `max_chars` (60 to 182, default 182). USSD carries at most 182 characters, and operators often cut lower: Africa's Talking's help centre gives 160 for Safaricom and for Nigerian networks and 184 for Airtel Kenya. Use `max_chars: 160` in Nigeria. Texts are plain ASCII, so every handset shows them and a screen's size is its length.
- **Reachability.** Every screen must be reachable from the start, every `next` must name a screen, and at least one confirm screen must exist.
- **Patterns and conditions.** Patterns must compile. Conditions must start with `=` and read only `trigger.body`: `env`, `steps`, `secrets` and the rest are refused.
- **Placeholders.** A placeholder must name an input some screen collects. `{{reference}}` is allowed only in `done` and the SMS texts.
- **Fit with the inputs schema.** When the workflow declares an object inputs schema, every collected name must be one of its properties, of a matching type (an integer property needs `validate.type: integer`), and every required property must be collected somewhere.
- **One workflow per service code** in each environment. A second workflow publishing the same code is refused with 409.

## Setting up

1. **Aggregator.** Get a USSD service code from Africa's Talking: a dedicated code, or a channel on a shared one ([needs people](needs-people.md#phase-3), W4). Its sandbox simulator works with the sandbox app.
2. **Channel.** An admin (`secret.manage`) connects the aggregator under **Settings > USSD channels**: pick the aggregator and the channel's environment, and optionally list the addresses callbacks must come from. The page shows the callback URL, with its token, **once**; copy it before dismissing it. The same page rotates the token (the old URL stops working at once), changes the environment or the allow-list, turns the channel off (callbacks get 404) or disconnects it, and lists the service codes each environment serves. The API does the same:

   ```
   PUT /v1/ussd/channels/africastalking
   {"environment": "prod", "allowed_cidrs": []}
   ```

   The answer holds `token` and `callback_url` **once**. Only the token's SHA-256 is kept. `rotate_token: true` makes a new token, after which the old one stops working; `disabled: true` turns the channel off (404); `DELETE` removes it. Changes reach other edge replicas within 30 seconds.
3. **Callback URL.** In the Africa's Talking dashboard (USSD > Service Codes > Callback), set the callback URL to `https://<edge host>/channels/ussd/<tenant id>/africastalking?token=<token>`.
4. **Workflow.** Publish a workflow with a `ussd` trigger for that service code. It answers in each environment it is deployed to; the channel's `environment` decides which one a callback reaches (`prod` by default, `dev` for a sandbox app).
5. **Outcome SMS.** For `notify.sms`, add an Africa's Talking connection (`africastalking@1`) in the same environment with a `sender_id`. Set `notify.connection` when the tenant has more than one connection.

`GET /v1/ussd` lists the channels (never their tokens) and the service codes each environment serves.

## Security model

Callers are the public: anyone who dials the code. The only identity is the number the aggregator reports. Threat model boundary B13 covers this.

- **Authenticating callbacks.** Africa's Talking does not sign USSD callbacks. A callback must carry the channel's token: 256 random bits, compared in constant time against its stored hash, before the request is parsed or anything is written. A channel may also list address ranges (`allowed_cidrs`) that callbacks must come from. On the edge, behind a load balancer, the address is the last `X-Forwarded-For` hop only when `TASKIEM_TRUST_PROXY` is set. The token is only as secret as the URL: keep callback URLs out of logs and tickets, make sure the ingress does not log query strings, and rotate the token if one leaks.
- **What the caller can do.** Only walk the menu of the workflow deployed for the dialled code, in the channel's environment. Only workflows with a `ussd` trigger answer: publishing one is the explicit act (`workflow.publish`), and the channel is a second (`secret.manage`). A caller has no roles and can reach nothing else: no other workflow, status or run data.
- **Inputs.** Each input is checked by its screen and the whole set against the workflow's inputs schema before the run starts. Inputs are typed values, never expressions.
- **Rate limits.** Per number: 12 callbacks at once, then one every 500 ms, per replica. Across replicas, through the database: 20 new sessions per number per 10 minutes and 5 confirmations per number per hour, per tenant. Per tenant: 400 callbacks at once, then 200 a second, per replica. Plan limits apply at run start; a refused start ends the session `refused` and, with `notify.sms`, tells the caller.
- **Personal data.** The caller's number is personal data. Before confirmation the session holds only a hash of the number salted with the tenant (pseudonymous, for the limits). At confirmation, the number and the inputs (fields marked `x-pii` and values recognised as personal) are sealed under subject keys, as in run history, so erasing a subject erases them too. The run's history holds the number sealed. The sealed data leaves the session once the outcome SMS is sent, or at the run's start when no SMS was asked for. Logs carry the session reference, never the number or the inputs. For aggregators that send only the latest input, the path so far is kept sealed in the session.
- **Retention.** Sessions are deleted a day after they expire. Outcomes still owed after a week are dropped with their data.

## Adapters for other aggregators

`engine/ussd.Adapter` is the seam:

```go
type Adapter interface {
	Name() string                                          // the {provider} in the URL
	Parse(r *http.Request, body []byte) (ussd.Request, error)
	Incremental() bool                                     // true: callbacks carry only the latest input
	Reply(w http.ResponseWriter, end bool, text string)
}
```

`connectors/africastalking.USSDAdapter` is the Africa's Talking one. Register others in `api.Server.USSD.Adapters`. For an incremental aggregator, the edge keeps the path in the session row, sealed, and reads it on every callback (no cache). A retried callback is then appended twice, so prefer aggregators that send the full path.

## Operations

- Callbacks are served by the **edge** role at `/channels/ussd/` (and by `all`). Hand-off backstops, outcome SMS and purges run in the **api** role (`RunUSSD`).
- Watch for "ussd:" warnings in the logs: lookups that failed, refused callbacks (wrong token or address), sessions that could not be confirmed in time. `taskiem_tenant_limit_hits_total{limit="ussd_rate"}` counts tenant floods.

## Limitations and follow-ups

- Input containing `*` cannot be typed: Africa's Talking uses it as the separator.
- When a caller dials a code with an extension (`*384*123*1#`), whether the extension arrives in `serviceCode` or in `text` is not confirmed (W4). Menus route by the exact service code.
- Africa's Talking's end-of-session notifications (Events URL) are not used.
- The result is not shown on a next screen: a run usually outlasts the session, so outcomes go by SMS.
- Menus are in the words the tenant writes (plain ASCII). Taskiem's own screens (busy, expired, ended, too many requests, not taken, not available) and the reconciliation SMS follow the tenant's default language when it is on, folded to ASCII ([Languages](languages.md)); callers cannot choose a language on USSD yet.

## Sources

Built only from Africa's Talking's public documentation and help centre, read 7 October 2026. developers.africastalking.com shows automated readers a browser challenge, so the callback fields and the `CON`/`END` answer were confirmed through search-engine extracts of its USSD pages and the help centre, read directly:

- https://developers.africastalking.com/docs/ussd/overview
- https://developers.africastalking.com/docs/ussd/handle_sessions
- https://help.africastalking.com/en/articles/1284071-what-is-the-duration-of-a-ussd-session-for-kenyan-telcos
- https://help.africastalking.com/en/articles/2298298-how-long-is-the-duration-of-a-ussd-session-for-nigerian-telcos
- https://help.africastalking.com/en/articles/1284096-what-is-the-character-limit-for-ussd-menus-and-user-input-in-kenya
- https://help.africastalking.com/en/articles/9915125-how-do-i-go-live-with-ussd
- https://help.africastalking.com/en/articles/5947646-how-can-ensure-that-the-post-request-to-my-callback-url-is-coming-from-africa-s-talking-and-not-some-other-place
