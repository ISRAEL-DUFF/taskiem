import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { get, post, postOnce, put, type ConnectorInfo } from "../api";
import { useAuth } from "../auth";
import {
  ErrorBox,
  Field,
  Items,
  PageHeader,
  Row,
  Skeleton,
  useAction,
  useLoad,
} from "../ui";
import { naira } from "../lib/invoices";
import { spacedNumber } from "../lib/schoolAccounts";
import {
  OWNER_TYPES,
  accountProblem,
  approvalLine,
  bankOf,
  dayName,
  ownerTypeShort,
  receiveHero,
  sweepLine,
  sweepRequest,
  turnOnBlocker,
  withdrawAmount,
  WEEKDAYS,
  type Balance,
  type Bank,
  type OwnerType,
  type ReceiveMoney,
  type SweepMode,
} from "../lib/receiveMoney";
import { plainly } from "./SchoolAccountParts";
import { Icon } from "../icons";
import { Copy } from "./Forms";
import type { ConnectionName } from "../forms/ConnectionPicker";

// Payments settings (decision 0052; docs/receive-money.md): receive money
// with Taskiem. A business with no payments account turns it on here and
// gets a wallet and an account number of its own, sees its balance, and
// withdraws to its bank under an approval.

const load = () => get<ReceiveMoney>("/v1/settings/receive-money");

export function PaymentsSettings() {
  const { me } = useAuth();
  const r = useLoad(load, []);
  const owner = !!me?.roles.includes("owner");
  const [news, setNews] = useState("");
  return (
    <>
      <p className="crumbs">
        <Link to="/settings" aria-label="Back to Settings">
          Settings
        </Link>
      </p>
      <PageHeader
        title="Payments"
        description="Receive money with Taskiem: an account number of your own that customers pay into, with no payments account to sign up for."
      />
      <ErrorBox error={r.error} />
      {news && (
        <div className="notice" role="status">
          {news}
        </div>
      )}
      {!r.data ? (
        <Skeleton rows={3} label="Loading your payments settings…" />
      ) : r.data.on ? (
        <>
          <ReceiveHero r={r.data} owner={owner} onDone={r.reload} />
          <Withdrawals
            r={r.data}
            owner={owner}
            onDone={(what) => (setNews(what), r.reload())}
          />
          <SweepToBank
            r={r.data}
            owner={owner}
            onDone={(what) => (setNews(what), r.reload())}
          />
        </>
      ) : (
        <TurnOn
          r={r.data}
          owner={owner}
          onDone={() => (
            setNews(
              "You can receive money now: share your account number below.",
            ),
            r.reload()
          )}
        />
      )}
    </>
  );
}

/** Turning it on: where the money is held, the business email and the director's BVN, sent once. */
function TurnOn({
  r,
  owner,
  onDone,
}: {
  r: ReceiveMoney;
  owner: boolean;
  onDone: () => void;
}) {
  const [ownerType, setOwnerType] = useState<OwnerType>(r.default_owner_type);
  const [email, setEmail] = useState("");
  const [bvn, setBvn] = useState("");
  const act = useAction();
  const blocker = turnOnBlocker(r, owner);
  return (
    <section
      className="card"
      aria-labelledby="receive-h"
      data-testid="receive-money-off"
    >
      <h2 id="receive-h">Receive money with Taskiem</h2>
      <p>
        Get an account number of your own today. Customers pay into it by bank
        transfer; each payment shows in Money, invoices are paid on it, and you
        withdraw to your own bank whenever you like.
      </p>
      {blocker ? (
        <div
          className="warning"
          role="note"
          data-testid="receive-money-blocked"
        >
          {blocker} {!r.plan && <Link to="/settings/billing">See plans</Link>}
        </div>
      ) : (
        <>
          <fieldset>
            <legend>Whose money will it hold?</legend>
            {OWNER_TYPES.map((o) => (
              <label className="row" key={o.value}>
                <input
                  type="radio"
                  name="owner-type"
                  checked={ownerType === o.value}
                  onChange={() => setOwnerType(o.value)}
                />{" "}
                <strong>{o.title}.</strong> {o.detail}
              </label>
            ))}
            <p className="hint">
              You choose this once. To change it later, ask Taskiem's support.
            </p>
          </fieldset>
          <Field label="Your business email">
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              autoComplete="email"
            />
          </Field>
          <Field
            label="The director's BVN"
            hint="11 digits. Sent to the partner bank once to open your account; Taskiem does not keep it."
          >
            <input
              value={bvn}
              onChange={(e) =>
                setBvn(e.target.value.replace(/\D/g, "").slice(0, 11))
              }
              inputMode="numeric"
              autoComplete="off"
            />
          </Field>
          <ErrorBox error={act.error} />
          <div className="toolbar">
            <button
              type="button"
              className="primary"
              disabled={act.busy || bvn.length !== 11 || !email.includes("@")}
              onClick={() =>
                void act.run(async () => {
                  await plainly(
                    post("/v1/settings/receive-money", {
                      owner_type: ownerType,
                      business_email: email,
                      bvn,
                    }),
                  );
                  setBvn("");
                  onDone();
                }, false)
              }
            >
              Turn it on
            </button>
          </div>
        </>
      )}
    </section>
  );
}

/** The business's own number on pine, with the balance and where the money is held. */
function ReceiveHero({
  r,
  owner,
  onDone,
}: {
  r: ReceiveMoney;
  owner: boolean;
  onDone: () => void;
}) {
  const h = receiveHero(r);
  const balance = useLoad(() => get<Balance>("/v1/receive-money/balance"), []);
  const act = useAction();
  return (
    <>
      <section
        className="hero"
        aria-label="Your account number"
        data-testid="receive-hero"
      >
        <div className="hero-top">
          <span className="hero-label">{h.label}</span>
          <span className="hero-tag">{h.tag}</span>
        </div>
        <div className="hero-amount words" data-testid="receive-number">
          {h.figure}
        </div>
        {h.sub && <p className="hero-sub">{h.sub}</p>}
        <dl className="hero-foot">
          <div>
            <dt>Balance</dt>
            <dd data-testid="receive-balance">
              {balance.data
                ? naira(balance.data.available_kobo)
                : balance.error
                  ? "Not available just now"
                  : "…"}
            </dd>
          </div>
          <div>
            <dt>Held</dt>
            <dd className="hero-foot-words">{ownerTypeShort(r.owner_type)}</dd>
          </div>
        </dl>
      </section>
      <div className="toolbar">
        {r.account && (
          <Copy
            text={r.account.account_number}
            label="Copy your account number"
          />
        )}
        {!r.account && owner && (
          <button
            type="button"
            disabled={act.busy}
            onClick={() =>
              void act.run(
                async () => (
                  await plainly(post("/v1/settings/receive-money", {})),
                  onDone()
                ),
                false,
              )
            }
          >
            Ask for the number again
          </button>
        )}
      </div>
      <ErrorBox error={act.error} />
      <p className="hint">
        Share this number with anyone who pays you. Payments into it show under{" "}
        <Link to="/invoices/match">To match</Link>; invoices you send get a
        number of their own.
      </p>
    </>
  );
}

/** Where withdrawals go and who approves them; and a withdrawal. */
function Withdrawals({
  r,
  owner,
  onDone,
}: {
  r: ReceiveMoney;
  owner: boolean;
  onDone: (news: string) => void;
}) {
  const { can } = useAuth();
  const [editing, setEditing] = useState(!r.withdraw_to);
  return (
    <section
      className="card"
      aria-labelledby="withdraw-h"
      id="withdraw"
      data-testid="withdrawals"
    >
      <h2 id="withdraw-h">Withdraw to your bank</h2>
      {r.withdraw_waiting && (
        <div className="warning" role="note" data-testid="withdraw-waiting">
          {r.withdraw_waiting}
        </div>
      )}
      {r.withdraw_to && !editing && (
        <Items label="Where withdrawals go">
          <Row
            icon="money"
            title={r.withdraw_to.account_name}
            sub={
              <span className="sub">
                {[
                  r.withdraw_to.bank_name,
                  spacedNumber(r.withdraw_to.account_number),
                ]
                  .filter(Boolean)
                  .join(" · ")}
                <span className="block">
                  {approvalLine(r.two_approvers_above_kobo)}
                </span>
              </span>
            }
            testid="withdraw-to"
          >
            {owner && (
              <button
                type="button"
                className="small"
                onClick={() => setEditing(true)}
              >
                Change
              </button>
            )}
          </Row>
        </Items>
      )}
      {editing &&
        (owner ? (
          <WithdrawTo
            r={r}
            onDone={() => (
              setEditing(false),
              onDone("Saved: withdrawals go to this account.")
            )}
          />
        ) : (
          <p className="hint">
            An owner chooses the bank account withdrawals go to.
          </p>
        ))}
      {r.withdraw_to && !editing && can("run.start") && <Withdraw />}
    </section>
  );
}

const SWEEP_MODES: { value: SweepMode; label: string }[] = [
  { value: "off", label: "Off: I withdraw when I choose" },
  { value: "weekly", label: "Every week" },
  { value: "above", label: "Whenever my wallet holds more than an amount" },
];

/** Sending what the wallet holds to the saved bank account automatically (N2): no approval, only to that account. */
function SweepToBank({
  r,
  owner,
  onDone,
}: {
  r: ReceiveMoney;
  owner: boolean;
  onDone: (news: string) => void;
}) {
  const s = r.sweep;
  const [editing, setEditing] = useState(false);
  const [mode, setMode] = useState<SweepMode>(s?.mode ?? "off");
  const [day, setDay] = useState(s?.day ?? "friday");
  const [time, setTime] = useState(s?.time ?? "17:00");
  const [above, setAbove] = useState(
    s?.above_kobo ? String(s.above_kobo / 100) : "",
  );
  const [keep, setKeep] = useState(
    s?.keep_kobo ? String(s.keep_kobo / 100) : "",
  );
  const act = useAction();
  const fee = s ? naira(s.fee_allowance_kobo) : "";
  return (
    <section className="card" aria-labelledby="sweep-h" data-testid="sweep">
      <h2 id="sweep-h">Send money to your bank automatically</h2>
      {s?.waiting && (
        <div className="warning" role="note" data-testid="sweep-waiting">
          {s.waiting}
        </div>
      )}
      {!r.withdraw_to ? (
        <p className="hint">
          Save the bank account above first: it is where the money goes.
        </p>
      ) : !editing ? (
        <>
          <p data-testid="sweep-line">{sweepLine(s)}</p>
          <p className="hint">
            Nobody approves it: it only ever pays your saved bank account,
            leaving {fee} for the bank's fee. The owner is told each time money
            is sent.
          </p>
          {owner && (
            <div className="toolbar">
              <button
                type="button"
                className="small"
                onClick={() => setEditing(true)}
              >
                {s?.mode === "off" ? "Turn on" : "Change"}
              </button>
            </div>
          )}
        </>
      ) : (
        <div data-testid="sweep-form">
          <fieldset>
            <legend>When</legend>
            {SWEEP_MODES.map((m) => (
              <label className="row" key={m.value}>
                <input
                  type="radio"
                  name="sweep-mode"
                  checked={mode === m.value}
                  onChange={() => setMode(m.value)}
                />{" "}
                {m.label}
              </label>
            ))}
          </fieldset>
          {mode === "weekly" && (
            <>
              <Field label="Day">
                <select value={day} onChange={(e) => setDay(e.target.value)}>
                  {WEEKDAYS.map((d) => (
                    <option key={d} value={d}>
                      {dayName(d)}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="Time (Lagos)">
                <input
                  type="time"
                  value={time}
                  onChange={(e) => setTime(e.target.value)}
                />
              </Field>
            </>
          )}
          {mode === "above" && (
            <Field
              label="When my wallet holds more than (₦)"
              hint="Checked every hour. At least ₦1,000."
            >
              <input
                value={above}
                onChange={(e) => setAbove(e.target.value)}
                inputMode="decimal"
                autoComplete="off"
              />
            </Field>
          )}
          {mode !== "off" && (
            <Field
              label="Keep in my wallet (₦, optional)"
              hint={`The rest goes to your bank, less ${fee} for the bank's fee, when it is at least ₦1,000.`}
            >
              <input
                value={keep}
                onChange={(e) => setKeep(e.target.value)}
                inputMode="decimal"
                autoComplete="off"
              />
            </Field>
          )}
          <ErrorBox error={act.error} />
          <div className="toolbar">
            <button
              type="button"
              className="primary"
              disabled={act.busy}
              onClick={() =>
                void act.run(async () => {
                  const req = sweepRequest(mode, day, time, above, keep);
                  if (!req.body) throw new Error(req.problem);
                  await plainly(
                    put("/v1/settings/receive-money/sweep", req.body),
                  );
                  setEditing(false);
                  onDone(
                    mode === "off"
                      ? "Turned off: your money stays in your wallet until you withdraw it."
                      : "Saved: your money goes to your bank as you chose.",
                  );
                }, false)
              }
            >
              Save
            </button>
            <button
              type="button"
              onClick={() => setEditing(false)}
              disabled={act.busy}
            >
              Cancel
            </button>
          </div>
        </div>
      )}
    </section>
  );
}

function WithdrawTo({ r, onDone }: { r: ReceiveMoney; onDone: () => void }) {
  const banks = useLoad(
    () => get<{ banks: Bank[] }>("/v1/receive-money/banks"),
    [],
  );
  const list = useMemo(
    () => (banks.data?.banks ?? []).map(bankOf).filter((b) => b.code),
    [banks.data],
  );
  const [code, setCode] = useState(r.withdraw_to?.bank_code ?? "");
  const [number, setNumber] = useState(r.withdraw_to?.account_number ?? "");
  const [two, setTwo] = useState(
    r.two_approvers_above_kobo ? String(r.two_approvers_above_kobo / 100) : "",
  );
  const act = useAction();
  const problem = number ? accountProblem(number) : "";
  return (
    <div data-testid="withdraw-to-form">
      <ErrorBox error={banks.error} />
      <Field label="Your bank">
        <select value={code} onChange={(e) => setCode(e.target.value)}>
          <option value="">Choose your bank</option>
          {list.map((b) => (
            <option key={b.code} value={b.code}>
              {b.name}
            </option>
          ))}
        </select>
      </Field>
      <Field
        label="Your account number"
        hint={
          problem || "We check the name on it with your bank before saving."
        }
      >
        <input
          value={number}
          onChange={(e) =>
            setNumber(e.target.value.replace(/\D/g, "").slice(0, 10))
          }
          inputMode="numeric"
          autoComplete="off"
        />
      </Field>
      <Field
        label="Two owners approve from (₦, optional)"
        hint="Below this, one owner approves a withdrawal. Leave it empty for one owner always."
      >
        <input
          value={two}
          onChange={(e) => setTwo(e.target.value)}
          inputMode="decimal"
          autoComplete="off"
        />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={act.busy || !code || number.length !== 10}
          onClick={() =>
            void act.run(async () => {
              const twoKobo = Math.round(
                Number(two.replace(/[₦,\s]/g, "") || "0") * 100,
              );
              if (!Number.isFinite(twoKobo) || twoKobo < 0)
                throw new Error(
                  "The amount for two owners is a number of naira, such as 500000.",
                );
              const bank = list.find((b) => b.code === code);
              await plainly(
                put("/v1/settings/receive-money/withdrawals", {
                  bank_code: code,
                  bank_name: bank?.name ?? "",
                  account_number: number,
                  two_approvers_above_kobo: twoKobo,
                }),
              );
              onDone();
            }, false)
          }
        >
          Save
        </button>
      </div>
    </div>
  );
}

function Withdraw() {
  const balance = useLoad(() => get<Balance>("/v1/receive-money/balance"), []);
  const [amount, setAmount] = useState("");
  const [key, setKey] = useState(() => crypto.randomUUID());
  const [run, setRun] = useState<string | null>(null);
  const act = useAction();
  const check = amount
    ? withdrawAmount(amount, balance.data?.available_kobo)
    : {};
  return (
    <div data-testid="withdraw">
      <Field
        label="Amount to withdraw (₦)"
        hint={
          check.problem ||
          (balance.data
            ? `Your wallet holds ${naira(balance.data.available_kobo)}; leave room for the bank's fee on the transfer.`
            : undefined)
        }
      >
        <input
          value={amount}
          onChange={(e) => {
            // Another amount is another withdrawal: never the last one's key.
            setAmount(e.target.value);
            setKey(crypto.randomUUID());
          }}
          inputMode="decimal"
          autoComplete="off"
        />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={act.busy || !check.kobo}
          onClick={() =>
            void act.run(async () => {
              const out = await plainly(
                postOnce<{ run_id: string }>(
                  "/v1/receive-money/withdraw",
                  key,
                  { amount_kobo: check.kobo },
                ),
              );
              setRun(out.run_id);
              setAmount("");
              setKey(crypto.randomUUID());
            }, false)
          }
        >
          Withdraw
        </button>
      </div>
      {run && (
        <div className="notice" role="status" data-testid="withdraw-started">
          Asked. Nothing is paid until it is approved: see{" "}
          <Link to="/approvals">Approvals</Link>, or{" "}
          <Link to={`/runs/${run}`}>follow the withdrawal</Link>.
        </div>
      )}
    </div>
  );
}

/** The business's own number on the Money page, once receiving money with Taskiem is on. */
export function ReceiveMoneyCard() {
  const r = useLoad(load, []);
  if (!r.data?.on) return null;
  const h = receiveHero(r.data);
  return (
    <section
      className="card"
      aria-labelledby="receive-card-h"
      data-testid="receive-card"
    >
      <h2 id="receive-card-h">Your account number</h2>
      <p className="receive-number" data-testid="receive-card-number">
        {h.figure}
      </p>
      {h.sub && <p className="hint">{h.sub}</p>}
      <div className="toolbar">
        {r.data.account && (
          <Copy
            text={r.data.account.account_number}
            label="Copy your account number"
          />
        )}
        <Link className="button small" to="/settings/payments#withdraw">
          Balance and withdrawals
        </Link>
      </div>
    </section>
  );
}

/** On Get started: receiving money with Taskiem, for a business with no payments account yet. */
export function ReceiveMoneyOffer() {
  const { me } = useAuth();
  const data = useLoad(
    () =>
      Promise.all([
        load(),
        get<{ connections: ConnectionName[] }>("/v1/connection-names"),
        get<{ connectors: ConnectorInfo[] }>("/v1/connectors"),
      ]),
    [],
  ).data;
  if (!data) return null;
  const [r, names, connectors] = data;
  // A payments account the business can be paid through (not one that only pays out or reads).
  const rails = new Set(
    connectors.connectors
      .filter((c) =>
        (c.capabilities ?? []).some(
          (x) => x === "collect_transfer" || x === "collect_link",
        ),
      )
      .map((c) => c.id),
  );
  const hasRail = names.connections.some(
    (n) =>
      n.environment === "prod" &&
      n.status === "active" &&
      rails.has(n.connector),
  );
  if (r.on || hasRail || !r.available) return null;
  const blocker = turnOnBlocker(r, !!me?.roles.includes("owner"));
  return (
    <section aria-labelledby="receive-offer-h" data-testid="receive-offer">
      <h2 id="receive-offer-h">Receive money from today</h2>
      <ul className="checklist">
        <li>
          <span className="list-icon">
            <Icon name="money" />
          </span>
          <div className="grow">
            <strong>An account number of your own</strong>
            <div className="hint">
              {blocker ||
                "No payments account to sign up for: customers pay you by transfer, and you withdraw to your bank."}
            </div>
          </div>
          {!blocker && (
            <Link className="button small" to="/settings/payments">
              Turn it on
            </Link>
          )}
          {!r.plan && (
            <Link className="button small" to="/settings/billing">
              See plans
            </Link>
          )}
        </li>
      </ul>
    </section>
  );
}
