// Staff and pay from Taskiem (P4b-2, docs/spec/chat-pay-people.md):
// Payroll → Team, Payroll → Account changes and My pay. Payrolla keeps the
// pay and the accounts; these screens read it when they open. An account
// number is typed here and goes to Payrolla once; a code goes to Payrolla
// once and is kept nowhere.

import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ApiError, get, patch, post } from "../api";
import { EmptyState, ErrorBox, Field, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { naira } from "../lib/invoices";
import { codeDigits, newKey } from "../lib/payroll";
import { MyPayslips } from "./Payslip";
import {
  NEW_CODE,
  addBody,
  addProblem,
  bankLine,
  changeBody,
  changeFormOf,
  emptyAdd,
  fromWords,
  isAccountNumber,
  accountDigits,
  masked,
  memberSub,
  splitName,
  viaWords,
  type AddForm,
  type BankChange,
  type BankChangesView,
  type MyCodeSent,
  type MyPay,
  type PayrollBank,
  type TeamMember,
  type TeamStaff,
  type TeamView,
} from "../lib/payrollteam";

function Crumbs() {
  return (
    <p className="crumbs">
      <Link to="/payroll" aria-label="Back to Payroll">
        Payroll
      </Link>
    </p>
  );
}

/** The bank picker and the account number, shared by every form that
 * takes a salary account. */
function AccountFields({ bankCode, account, onBank, onAccount }: { bankCode: string; account: string; onBank: (c: string) => void; onAccount: (a: string) => void }) {
  const banks = useLoad(() => get<{ banks: PayrollBank[] }>("/v1/payroll/banks"), []);
  return (
    <>
      <Field label="Bank">
        <select value={bankCode} onChange={(e) => onBank(e.target.value)} data-testid="payroll-bank">
          <option value="">Choose the bank</option>
          {(banks.data?.banks ?? []).map((b) => (
            <option key={b.code} value={b.code}>
              {b.name}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Account number">
        <input inputMode="numeric" maxLength={13} value={account} onChange={(e) => onAccount(e.target.value)} data-testid="payroll-account" />
      </Field>
    </>
  );
}

/** The bank's name for an account, shown large before it is saved. */
function NameCheck({ bankCode, account }: { bankCode: string; account: string }) {
  const [shown, setShown] = useState<{ checked: boolean; account_name?: string; words?: string }>();
  const check = useAction();
  useEffect(() => setShown(undefined), [bankCode, account]);
  const ready = !!bankCode && isAccountNumber(account);
  return (
    <div data-testid="payroll-name-check">
      {shown?.checked && (
        <div className="notice" role="status">
          <span className="hint">The bank's name for this account</span>
          <p className="hero-amount" style={{ margin: 0, fontSize: "1.4rem" }} data-testid="payroll-account-name">
            {shown.account_name}
          </p>
        </div>
      )}
      {shown && !shown.checked && (
        <p className="hint" role="status">
          {shown.words}
        </p>
      )}
      <ErrorBox error={check.error} />
      {!shown && (
        <button
          type="button"
          className="small"
          disabled={!ready || check.busy}
          data-testid="payroll-check-account"
          onClick={() =>
            void check.run(async () => {
              setShown(await post("/v1/payroll/name-check", { bank_code: bankCode, account_number: accountDigits(account) }));
            }, false)
          }
        >
          {check.busy ? "Checking…" : "Check the account"}
        </button>
      )}
    </div>
  );
}

/** Add someone (payroll.prepare), from a staff member when one is given. */
function AddSheet({ from, onClose, onAdded }: { from?: TeamStaff; onClose: () => void; onAdded: () => void }) {
  const [f, setF] = useState<AddForm>(() => {
    const base = emptyAdd();
    if (!from) return base;
    const n = splitName(from.name);
    return { ...base, first: n.first, last: n.last, email: from.email ?? "", phone: from.phone ?? "", staffUser: from.user_id };
  });
  const [key] = useState(newKey);
  const [parts, setParts] = useState(false);
  const [done, setDone] = useState<TeamMember>();
  const save = useAction();
  const set = (k: keyof AddForm) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });
  const problem = addProblem(f);
  if (done)
    return (
      <Modal title="Added to payroll" onClose={onAdded}>
        <p role="status" data-testid="payroll-added">
          {done.name} is in Payrolla. They're paid into:
        </p>
        <p className="hero-amount" style={{ fontSize: "1.4rem", margin: 0 }} data-testid="payroll-added-account">
          {done.bank?.account_name}
        </p>
        <p className="hint">{bankLine(done.bank)}</p>
        <div className="toolbar">
          <button type="button" className="primary" onClick={onAdded}>
            Done
          </button>
        </div>
      </Modal>
    );
  return (
    <Modal title={from ? `Add ${from.name} to payroll` : "Add someone to payroll"} onClose={onClose}>
      <Field label="First name">
        <input value={f.first} maxLength={80} onChange={set("first")} data-testid="payroll-first" />
      </Field>
      <Field label="Last name">
        <input value={f.last} maxLength={80} onChange={set("last")} data-testid="payroll-last" />
      </Field>
      <Field label="Phone" hint="A phone or an email is needed, for their payslip.">
        <input type="tel" value={f.phone} onChange={set("phone")} data-testid="payroll-phone" />
      </Field>
      <Field label="Email">
        <input type="email" value={f.email} onChange={set("email")} data-testid="payroll-email" />
      </Field>
      <Field label="Start date">
        <input type="date" value={f.start} onChange={set("start")} data-testid="payroll-start" />
      </Field>
      <Field label="Department">
        <input value={f.department} maxLength={80} onChange={set("department")} data-testid="payroll-department" />
      </Field>
      <Field label="Monthly pay (₦)">
        <input inputMode="decimal" value={f.gross} onChange={set("gross")} data-testid="payroll-gross" />
      </Field>
      {!parts ? (
        <button type="button" className="quiet small" onClick={() => setParts(true)} data-testid="payroll-parts">
          Split into basic, housing and transport
        </button>
      ) : (
        <>
          <Field label="Basic (₦)">
            <input inputMode="decimal" value={f.basic} onChange={set("basic")} />
          </Field>
          <Field label="Housing (₦)">
            <input inputMode="decimal" value={f.housing} onChange={set("housing")} />
          </Field>
          <Field label="Transport (₦)">
            <input inputMode="decimal" value={f.transport} onChange={set("transport")} />
          </Field>
        </>
      )}
      <label className="batch-pick">
        <input type="checkbox" checked={f.pensionable} onChange={(e) => setF({ ...f, pensionable: e.target.checked })} /> Pension is taken from this pay
      </label>
      <AccountFields bankCode={f.bankCode} account={f.account} onBank={(c) => setF({ ...f, bankCode: c })} onAccount={(a) => setF({ ...f, account: a })} />
      <NameCheck bankCode={f.bankCode} account={f.account} />
      {problem && <p className="hint">{problem}</p>}
      <ErrorBox error={save.error} />
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={!!problem || save.busy}
          data-testid="payroll-add-save"
          onClick={() =>
            void save.run(async () => {
              const r = await post<{ employee: TeamMember }>("/v1/payroll/team", addBody(f, key));
              setDone(r.employee);
            }, false)
          }
        >
          {save.busy ? "Adding…" : "Add to payroll"}
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** Change someone's details or pay (payroll.prepare). */
function ChangeSheet({ m, onClose, onDone }: { m: TeamMember; onClose: () => void; onDone: (words?: string) => void }) {
  const [f, setF] = useState(() => changeFormOf(m));
  const [key] = useState(newKey);
  const save = useAction();
  const body = changeBody(m, f, key);
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });
  return (
    <Modal title={`Change ${m.name}`} onClose={onClose}>
      <Field label="First name">
        <input value={f.first} onChange={set("first")} />
      </Field>
      <Field label="Last name">
        <input value={f.last} onChange={set("last")} />
      </Field>
      <Field label="Email">
        <input type="email" value={f.email} onChange={set("email")} />
      </Field>
      <Field label="Department">
        <input value={f.department} onChange={set("department")} data-testid="payroll-change-department" />
      </Field>
      <Field label="Monthly pay (₦)">
        <input inputMode="decimal" value={f.gross} onChange={set("gross")} data-testid="payroll-change-gross" />
      </Field>
      <Field label="New pay starts" hint={fromWords(f.from)}>
        <input type="date" value={f.from} onChange={set("from")} data-testid="payroll-change-from" />
      </Field>
      <p className="hint">
        Phone: {m.phone || "none"}. A phone number is changed in Payrolla. Payrolla keeps no job title.
      </p>
      {typeof body === "string" && <p className="hint">{body}</p>}
      <ErrorBox error={save.error} />
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={!body || typeof body === "string" || save.busy}
          data-testid="payroll-change-save"
          onClick={() =>
            void save.run(async () => {
              const r = await patch<{ words?: string }>(`/v1/payroll/team/${encodeURIComponent(m.id)}`, body);
              onDone(r.words);
            }, false)
          }
        >
          Save
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** A new salary account, held for another approver. */
function BankSheet({ m, onClose, onDone }: { m: TeamMember; onClose: () => void; onDone: (words: string) => void }) {
  const [bankCode, setBank] = useState("");
  const [account, setAccount] = useState("");
  const [key] = useState(newKey);
  const save = useAction();
  return (
    <Modal title={`New salary account for ${m.name}`} onClose={onClose}>
      <p className="hint">It waits until another approver approves it. They're paid into {m.bank ? masked(m.bank.last4) : "their current account"} until then.</p>
      <AccountFields bankCode={bankCode} account={account} onBank={setBank} onAccount={setAccount} />
      <NameCheck bankCode={bankCode} account={account} />
      <ErrorBox error={save.error} />
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={!bankCode || !isAccountNumber(account) || save.busy}
          data-testid="payroll-bank-save"
          onClick={() =>
            void save.run(async () => {
              const r = await post<{ words: string }>(`/v1/payroll/team/${encodeURIComponent(m.id)}/bank`, { key, bank_code: bankCode, account_number: accountDigits(account) });
              onDone(r.words);
            }, false)
          }
        >
          Ask for the change
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** End someone's employment: today, as Payrolla can't set a last day. */
function EndSheet({ m, onClose, onDone }: { m: TeamMember; onClose: () => void; onDone: (words: string) => void }) {
  const [key] = useState(newKey);
  const end = useAction();
  return (
    <Modal title={`End ${m.name}'s employment?`} onClose={onClose}>
      <div className="warning" role="note">
        This takes effect today. Payrolla can't set a last day. They're left out of payroll worked out from now on.
      </div>
      <ErrorBox error={end.error} />
      <div className="toolbar">
        <button
          type="button"
          className="danger"
          disabled={end.busy}
          data-testid="payroll-end-yes"
          onClick={() =>
            void end.run(async () => {
              const r = await post<{ words: string }>(`/v1/payroll/team/${encodeURIComponent(m.id)}/end`, { key, confirm: "yes" });
              onDone(r.words);
            }, false)
          }
        >
          Yes, end it today
        </button>
        <button type="button" onClick={onClose}>
          Keep them
        </button>
      </div>
    </Modal>
  );
}

type Sheet = { kind: "change" | "bank" | "end"; m: TeamMember } | { kind: "add"; from?: TeamStaff };

/** /payroll/team: everyone in Payrolla. */
export function PayrollTeamPage() {
  const view = useLoad(() => get<TeamView>("/v1/payroll/team"), []);
  const [params, setParams] = useSearchParams();
  const [sheet, setSheet] = useState<Sheet>();
  const [said, setSaid] = useState("");
  const d = view.data;
  const addFor = params.get("add");
  useEffect(() => {
    if (!d || !addFor) return;
    const st = d.staff.find((s) => s.user_id === addFor);
    if (st && d.may.prepare) setSheet({ kind: "add", from: st });
    setParams({}, { replace: true });
  }, [d, addFor, setParams]);
  const finish = (words?: string) => {
    setSheet(undefined);
    if (words) setSaid(words);
    view.reload();
  };
  return (
    <>
      <Crumbs />
      <PageHeader
        title="Team"
        description="Everyone in your Payrolla, and the account each is paid into."
        actions={
          d?.may.prepare && (
            <button type="button" className="primary" onClick={() => setSheet({ kind: "add" })} data-testid="payroll-add">
              Add someone
            </button>
          )
        }
      />
      {said && (
        <div className="notice" role="status" data-testid="payroll-said">
          {said}
        </div>
      )}
      <ErrorBox error={view.error} />
      {!d && !view.error && <Skeleton rows={4} label="Loading your team…" />}
      {d && d.team.length === 0 && <EmptyState icon="members">No one is in your Payrolla yet.</EmptyState>}
      {d && d.team.length > 0 && (
        <ul className="items" aria-label="Team" data-testid="payroll-team">
          {d.team.map((m) => (
            <Row
              key={m.id}
              letter={m.name.charAt(0)}
              tone={m.waiting_change ? "warn" : m.status === "left" ? "muted" : undefined}
              title={m.name}
              side={m.pay ? naira(m.pay.gross_kobo) : undefined}
              sub={
                <div className="sub">
                  {memberSub(m)}
                  {m.staff ? " · In your staff" : ""}
                </div>
              }
              testid="payroll-member"
            >
              {d.may.prepare && m.status !== "left" && (
                <div className="toolbar">
                  <button type="button" className="small" onClick={() => setSheet({ kind: "change", m })} data-testid="payroll-change">
                    Change<span className="sr-only"> {m.name}</span>
                  </button>
                  <button type="button" className="small" onClick={() => setSheet({ kind: "bank", m })} data-testid="payroll-new-account">
                    New account<span className="sr-only"> for {m.name}</span>
                  </button>
                  <button type="button" className="small quiet" onClick={() => setSheet({ kind: "end", m })} data-testid="payroll-end">
                    End employment<span className="sr-only"> of {m.name}</span>
                  </button>
                </div>
              )}
            </Row>
          ))}
        </ul>
      )}
      {d && d.may.prepare && d.staff.length > 0 && (
        <section className="card" aria-labelledby="payroll-not-in-h">
          <h2 id="payroll-not-in-h">Staff not in Payrolla</h2>
          <ul className="items" aria-label="Staff not in Payrolla">
            {d.staff.map((s) => (
              <Row key={s.user_id} letter={s.name.charAt(0) || "?"} title={s.name || s.email || "Someone"} testid="payroll-staff-not-in">
                <div className="toolbar">
                  <button type="button" className="small" onClick={() => setSheet({ kind: "add", from: s })} data-testid="payroll-add-staff">
                    Add to payroll<span className="sr-only"> {s.name}</span>
                  </button>
                </div>
              </Row>
            ))}
          </ul>
        </section>
      )}
      {sheet?.kind === "add" && <AddSheet from={sheet.from} onClose={() => setSheet(undefined)} onAdded={() => finish()} />}
      {sheet?.kind === "change" && <ChangeSheet m={sheet.m} onClose={() => setSheet(undefined)} onDone={finish} />}
      {sheet?.kind === "bank" && <BankSheet m={sheet.m} onClose={() => setSheet(undefined)} onDone={finish} />}
      {sheet?.kind === "end" && <EndSheet m={sheet.m} onClose={() => setSheet(undefined)} onDone={finish} />}
    </>
  );
}

/** Approve a change with the code Payrolla sends the approver. */
function ApproveSheet({ c, onClose, onDone }: { c: BankChange; onClose: () => void; onDone: (words: string) => void }) {
  const [sent, setSent] = useState<{ challenge: { challenge_id: string }; sent_to: string; safer: boolean }>();
  const [code, setCode] = useState("");
  const [needNew, setNeedNew] = useState(false);
  const send = useAction();
  const confirm = useAction();
  const path = `/v1/payroll/bank-changes/${encodeURIComponent(c.id)}`;
  const sendCode = () =>
    void send.run(async () => {
      setNeedNew(false);
      setCode("");
      confirm.clear();
      setSent(await post(`${path}/code`, { key: newKey() }));
    }, false);
  const typed = codeDigits(code);
  return (
    <Modal title={`Approve ${c.employee ?? "this"} account change`} onClose={onClose}>
      <p>
        New account: <strong>{c.account_name}</strong>, {c.bank} {masked(c.last4)}. {viaWords(c)}.
      </p>
      {!sent ? (
        <>
          <ErrorBox error={send.error} />
          <div className="toolbar">
            <button type="button" className="primary" disabled={send.busy} onClick={sendCode} data-testid="payroll-approve-send">
              {send.busy ? "Sending…" : "Send me a code"}
            </button>
            <button type="button" onClick={onClose}>
              Cancel
            </button>
          </div>
        </>
      ) : (
        <>
          <p role="status" data-testid="payroll-approve-sent">
            {sent.sent_to} Type it here.
          </p>
          {sent.safer && <p className="hint">A code from an authenticator app is safer: set one up in Payrolla.</p>}
          <Field label="The code from Payrolla">
            <input inputMode="numeric" autoComplete="one-time-code" maxLength={7} value={code} onChange={(e) => setCode(e.target.value.replace(/[^\d ]/g, ""))} data-testid="payroll-approve-code" />
          </Field>
          <ErrorBox error={confirm.error} />
          <div className="toolbar">
            {!needNew && (
              <button
                type="button"
                className="primary"
                disabled={confirm.busy || typed.length !== 6}
                data-testid="payroll-approve-confirm"
                onClick={() =>
                  void confirm.run(async () => {
                    try {
                      const r = await post<{ words: string }>(`${path}/approve`, { challenge_id: sent.challenge.challenge_id, code: typed, key: newKey() });
                      onDone(r.words);
                    } catch (e) {
                      if (e instanceof ApiError && NEW_CODE.has(String(e.body.code ?? ""))) setNeedNew(true);
                      throw e;
                    }
                  }, false)
                }
              >
                Approve
              </button>
            )}
            <button type="button" className={needNew ? "primary" : "quiet"} disabled={send.busy} onClick={sendCode} data-testid="payroll-approve-resend">
              Send a new code
            </button>
          </div>
        </>
      )}
    </Modal>
  );
}

/** Reject a change with a short note. */
function RejectSheet({ c, onClose, onDone }: { c: BankChange; onClose: () => void; onDone: (words: string) => void }) {
  const [note, setNote] = useState("");
  const [key] = useState(newKey);
  const go = useAction();
  return (
    <Modal title="Reject this account change?" onClose={onClose}>
      <p>
        {c.employee} stays on their current account. {c.account_name}, {c.bank} {masked(c.last4)} is not used.
      </p>
      <Field label="Why, in a few words">
        <input value={note} maxLength={300} onChange={(e) => setNote(e.target.value)} data-testid="payroll-reject-note" />
      </Field>
      <ErrorBox error={go.error} />
      <div className="toolbar">
        <button
          type="button"
          className="danger"
          disabled={!note.trim() || go.busy}
          data-testid="payroll-reject-yes"
          onClick={() =>
            void go.run(async () => {
              const r = await post<{ words: string }>(`/v1/payroll/bank-changes/${encodeURIComponent(c.id)}/reject`, { note: note.trim(), key });
              onDone(r.words);
            }, false)
          }
        >
          Reject
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** /payroll/account-changes: what is waiting for an approver. */
export function PayrollChangesPage() {
  const view = useLoad(() => get<BankChangesView>("/v1/payroll/bank-changes"), []);
  const [sheet, setSheet] = useState<{ kind: "approve" | "reject"; c: BankChange }>();
  const [said, setSaid] = useState("");
  const d = view.data;
  const finish = (words: string) => {
    setSheet(undefined);
    setSaid(words);
    view.reload();
  };
  return (
    <>
      <Crumbs />
      <PageHeader title="Account changes" description="New salary accounts wait here until an approver other than whoever asked approves them." />
      {said && (
        <div className="notice" role="status" data-testid="payroll-said">
          {said}
        </div>
      )}
      <ErrorBox error={view.error} />
      {!d && !view.error && <Skeleton rows={3} label="Loading account changes…" />}
      {d && d.changes.length === 0 && <EmptyState icon="check">No account change is waiting.</EmptyState>}
      {d && d.changes.length > 0 && (
        <ul className="items" aria-label="Account changes waiting" data-testid="payroll-changes">
          {d.changes.map((c) => (
            <Row
              key={c.id}
              letter={(c.employee ?? "?").charAt(0)}
              tone="warn"
              title={c.employee ?? "Someone"}
              sub={
                <div className="sub">
                  {c.account_name}, {c.bank} {masked(c.last4)} · {viaWords(c)}
                </div>
              }
              testid="payroll-change-row"
            >
              {d.may.approve && (
                <div className="toolbar">
                  {c.mine ? (
                    <span className="hint">You asked for this, so someone else must approve it.</span>
                  ) : (
                    <button type="button" className="small primary" onClick={() => setSheet({ kind: "approve", c })} data-testid="payroll-change-approve">
                      Approve<span className="sr-only"> {c.employee}</span>
                    </button>
                  )}
                  <button type="button" className="small quiet" onClick={() => setSheet({ kind: "reject", c })} data-testid="payroll-change-reject">
                    Reject<span className="sr-only"> {c.employee}</span>
                  </button>
                </div>
              )}
            </Row>
          ))}
        </ul>
      )}
      {sheet?.kind === "approve" && <ApproveSheet c={sheet.c} onClose={() => setSheet(undefined)} onDone={finish} />}
      {sheet?.kind === "reject" && <RejectSheet c={sheet.c} onClose={() => setSheet(undefined)} onDone={finish} />}
    </>
  );
}

/** Change my salary account: the bank's name for it first, then the code
 * Payrolla texts me. */
function MyAccountSheet({ onClose, onDone }: { onClose: () => void; onDone: (words: string) => void }) {
  const [bankCode, setBank] = useState("");
  const [account, setAccount] = useState("");
  const [sent, setSent] = useState<MyCodeSent>();
  const [code, setCode] = useState("");
  const [needNew, setNeedNew] = useState(false);
  const send = useAction();
  const confirm = useAction();
  const typed = codeDigits(code);
  const sendCode = () =>
    void send.run(async () => {
      setNeedNew(false);
      setCode("");
      confirm.clear();
      setSent(await post<MyCodeSent>("/v1/payroll/me/bank-change", { key: newKey(), bank_code: bankCode, account_number: accountDigits(account) }));
    }, false);
  return (
    <Modal title="Change my salary account" onClose={onClose}>
      {!sent ? (
        <>
          <AccountFields bankCode={bankCode} account={account} onBank={setBank} onAccount={setAccount} />
          <p className="hint">Payrolla checks the account with the bank and texts you a code.</p>
          <ErrorBox error={send.error} />
          <div className="toolbar">
            <button type="button" className="primary" disabled={!bankCode || !isAccountNumber(account) || send.busy} onClick={sendCode} data-testid="mypay-send">
              {send.busy ? "Checking…" : "Continue"}
            </button>
            <button type="button" onClick={onClose}>
              Cancel
            </button>
          </div>
        </>
      ) : (
        <>
          <div className="notice">
            <span className="hint">Is this your account?</span>
            <p className="hero-amount" style={{ fontSize: "1.4rem", margin: 0 }} data-testid="mypay-account-name">
              {sent.account_name} {masked(sent.last4)}
            </p>
            <span className="hint">{sent.bank}</span>
          </div>
          <p role="status" data-testid="mypay-sent">
            {sent.sent_to} If this is your account, type the code here.
          </p>
          <Field label="The code from Payrolla">
            <input inputMode="numeric" autoComplete="one-time-code" maxLength={7} value={code} onChange={(e) => setCode(e.target.value.replace(/[^\d ]/g, ""))} data-testid="mypay-code" />
          </Field>
          <ErrorBox error={confirm.error} />
          <div className="toolbar">
            {!needNew && (
              <button
                type="button"
                className="primary"
                disabled={confirm.busy || typed.length !== 6}
                data-testid="mypay-confirm"
                onClick={() =>
                  void confirm.run(async () => {
                    try {
                      const r = await post<{ words: string }>("/v1/payroll/me/bank-change/confirm", { challenge_id: sent.challenge.challenge_id, code: typed, key: newKey() });
                      onDone(r.words);
                    } catch (e) {
                      if (e instanceof ApiError && NEW_CODE.has(String(e.body.code ?? ""))) setNeedNew(true);
                      throw e;
                    }
                  }, false)
                }
              >
                Yes, it's mine
              </button>
            )}
            <button type="button" className={needNew ? "primary" : "quiet"} disabled={send.busy} onClick={sendCode} data-testid="mypay-resend">
              Send a new code
            </button>
            <button type="button" onClick={onClose}>
              It's not mine
            </button>
          </div>
        </>
      )}
    </Modal>
  );
}

/** /payroll/me: a staff member's own pay account in Payrolla. */
export function MyPayPage() {
  const view = useLoad(() => get<MyPay>("/v1/payroll/me"), []);
  const [changing, setChanging] = useState(false);
  const [said, setSaid] = useState("");
  const me = view.data?.me;
  return (
    <>
      <PageHeader title="My pay" description="The account your salary is paid into, as Payrolla has it." />
      {said && (
        <div className="notice" role="status" data-testid="payroll-said">
          {said}
        </div>
      )}
      <ErrorBox error={view.error} />
      {!view.data && !view.error && <Skeleton rows={2} label="Loading…" />}
      {view.data && !view.data.linked && (
        <EmptyState icon="billing">You're not linked to Payrolla in this business yet. Ask the owner to add you to payroll.</EmptyState>
      )}
      {me && (
        <section className="card" aria-labelledby="mypay-h" data-testid="mypay">
          <h2 id="mypay-h" style={{ marginTop: 0 }}>
            Salary account
          </h2>
          <p className="hero-amount" style={{ fontSize: "1.4rem", margin: 0 }}>
            {me.bank?.account_name ?? "No account yet"}
          </p>
          <p className="hint">{bankLine(me.bank)}</p>
          {me.waiting_change && (
            <div className="notice" role="note" data-testid="mypay-waiting">
              Waiting for your employer to approve: {me.waiting_change.account_name}, {me.waiting_change.bank} {masked(me.waiting_change.last4)}.
            </div>
          )}
          <dl className="kv">
            <dt>Your phone in Payrolla</dt>
            <dd>{me.phone || "None"}</dd>
          </dl>
          <p className="hint">Payrolla texts your codes to this phone. Your employer changes it in Payrolla.</p>
          {me.status !== "left" && (
            <div className="toolbar">
              <button type="button" className="primary" onClick={() => setChanging(true)} data-testid="mypay-change">
                Change my salary account
              </button>
            </div>
          )}
        </section>
      )}
      {me && <MyPayslips />}
      {changing && (
        <MyAccountSheet
          onClose={() => setChanging(false)}
          onDone={(w) => {
            setChanging(false);
            setSaid(w);
            view.reload();
          }}
        />
      )}
    </>
  );
}
