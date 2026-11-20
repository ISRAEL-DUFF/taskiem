import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, del, get, post, put } from "../api";
import { Icon } from "../icons";
import { useAuth } from "../auth";
import { assert } from "../passkeys";
import { EmptyState, ErrorBox, Field, Items, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { useConfirm } from "./ConfirmSheet";
import {
  bankFields,
  bankValue,
  bankValueOf,
  checkLine,
  cleanName,
  cleanNumber,
  nameProblem,
  numberProblem,
  providerLine,
  savedBase,
  savedSub,
  showConnectStep,
  type BankList,
  type SavedList,
  type SavedProvider,
  type SavedThing,
} from "../lib/saved";

// Saved things, S0 (docs/spec/saved-objects.md; build plan S0): saved bank
// accounts, called up by @name. A business's are on Settings > Saved
// (everyone sees them, masked; those who manage them add, change and
// remove); a person's own are on You's settings. Changing an account and
// seeing its number in full ask for a passkey.

/** Settings > Saved: the business's saved accounts, and the step to
 * connect a payments account so their names are checked with the bank. */
export function SavedPage() {
  const provider = useLoad(() => get<SavedProvider>("/v1/saved/provider"), []);
  return (
    <>
      <p className="crumbs">
        <Link to="/settings" aria-label="Back to Settings">
          Settings
        </Link>
      </p>
      <PageHeader
        title="Saved"
        description="Accounts your business pays or shares, saved once and called up by name, like @bayo. Account numbers are kept sealed and shown masked."
      />
      <ErrorBox error={provider.error} />
      {provider.data && showConnectStep(provider.data) && <ConnectStep p={provider.data} />}
      <SavedCard mine={false} provider={provider.data} />
    </>
  );
}

/** The step to connect a payments account (owners of a business with
 * none): a Taskiem wallet first, or an account the business already has. */
function ConnectStep({ p }: { p: SavedProvider }) {
  const wallet = p.wallet;
  return (
    <section className="card" aria-labelledby="saved-connect-h" data-testid="saved-connect">
      <h2 id="saved-connect-h">Check names with the bank</h2>
      <p className="hint">{p.words ?? "Connect a payments account to check the bank's name for each account you save."}</p>
      {wallet && !wallet.plan && wallet.plan_note && (
        <div className="warning" role="note">
          {wallet.plan_note} <Link to="/settings/billing">See plans</Link>
        </div>
      )}
      <div className="toolbar">
        {wallet?.available && wallet.plan && (
          <Link className="button primary" to="/settings/payments" data-testid="saved-open-wallet">
            Open a Taskiem wallet
          </Link>
        )}
        <Link className="button" to="/connections" data-testid="saved-have-account">
          I already have an account
        </Link>
      </div>
      {p.providers && p.providers.length > 0 && <p className="hint">With {p.providers.map((x) => x.name).join(", ")}.</p>}
    </section>
  );
}

/** One owner's saved accounts as rows: the person's own (mine) on You, or
 * the business's. */
export function SavedCard({ mine, provider }: { mine: boolean; provider?: SavedProvider }) {
  const base = savedBase(mine);
  const list = useLoad(() => get<SavedList>(base), [base]);
  const [editing, setEditing] = useState<SavedThing | "new">();
  const [shown, setShown] = useState<Record<string, string>>({});
  const [news, setNews] = useState("");
  const act = useAction();
  const sure = useConfirm();
  const all = list.data?.saved ?? [];
  const { can } = useAuth();
  const manage = mine || can("saved.manage");

  // Accounts saved before a payments account was connected are checked as
  // soon as one is.
  const checked = useRef(false);
  useEffect(() => {
    if (mine || !list.data?.can_check || checked.current) return;
    checked.current = true;
    void post<{ checked: number }>("/v1/saved/check").then(
      (r) => {
        if (r.checked > 0) setNews(r.checked === 1 ? "1 account's name was checked with the bank." : `${r.checked} accounts' names were checked with the bank.`);
        list.reload();
      },
      () => undefined,
    );
  }, [mine, list.data?.can_check, list]);

  const remove = async (x: SavedThing) => {
    const yes = await sure.confirm({ title: `Remove @${x.name}?`, body: "Its name is free to use again.", ok: "Remove", danger: true });
    if (!yes) return;
    await act.run(async () => {
      await del(`${base}/${encodeURIComponent(x.id)}`);
      list.reload();
    }, `@${x.name} removed`);
  };
  const reveal = (x: SavedThing) =>
    void act.run(async () => {
      const passkey = await assert(`${base}/step-up/options`, { operation: "saved.reveal", target: x.id });
      const r = await post<{ account_number: string }>(`${base}/${encodeURIComponent(x.id)}/reveal`, { passkey });
      setShown((s) => ({ ...s, [x.id]: r.account_number }));
    }, false);

  const title = mine ? "Your saved accounts" : "Saved accounts";
  const line = providerLine(provider);
  return (
    <section className="card" aria-labelledby={`saved-h-${mine ? "mine" : "biz"}`} data-testid={mine ? "my-saved" : "saved"}>
      <div className="toolbar">
        <h2 id={`saved-h-${mine ? "mine" : "biz"}`} className="grow" style={{ margin: 0 }}>
          {title}
        </h2>
        {list.data && manage && all.length < list.data.limit && (
          <button type="button" className="small" onClick={() => setEditing("new")} data-testid="saved-add">
            <Icon name="plus" /> Save an account
          </button>
        )}
      </div>
      <p className="hint">
        {mine
          ? "Accounts of your own, like @mum. Only you see them: no business you work in sees this list."
          : "Call one up by its name, like @bayo. Everyone in the business sees them, with the number masked."}
      </p>
      {line && <p className="hint">{line}</p>}
      {news && (
        <div className="notice" role="status">
          {news}
        </div>
      )}
      <ErrorBox error={list.error ?? act.error} />
      {!list.data && !list.error && <Skeleton rows={2} label="Loading the saved accounts…" />}
      {list.data && all.length === 0 && <EmptyState icon="money">No saved accounts yet.</EmptyState>}
      {all.length > 0 && (
        <Items label={title}>
          {all.map((x) => {
            const c = checkLine(x);
            return (
              <Row
                key={x.id}
                icon="money"
                tone={c.tone === "warn" ? "warn" : undefined}
                title={x.title}
                side={<span className={`badge sf-status ${c.tone}`}>{c.tone === "ok" ? "Checked" : "Not checked"}</span>}
                sub={
                  <span className="sub">
                    {savedSub(x)}
                    <span className="block" data-testid="saved-check">
                      {c.words}
                    </span>
                    {shown[x.id] && (
                      <span className="block" data-testid="saved-number">
                        Account number: <strong>{shown[x.id]}</strong>
                      </span>
                    )}
                    {x.details.note && <span className="block">{x.details.note}</span>}
                  </span>
                }
                testid="saved-row"
                attrs={{ "data-name": x.name }}
              >
                {x.to_change && (
                  <>
                    <button type="button" className="small" onClick={() => setEditing(x)}>
                      Change<span className="sr-only"> @{x.name}</span>
                    </button>
                    {!shown[x.id] && (
                      <button type="button" className="small" disabled={act.busy} onClick={() => reveal(x)}>
                        Show number<span className="sr-only"> of @{x.name}</span>
                      </button>
                    )}
                    <button type="button" className="small danger" disabled={act.busy} onClick={() => void remove(x)}>
                      Remove<span className="sr-only"> @{x.name}</span>
                    </button>
                  </>
                )}
              </Row>
            );
          })}
        </Items>
      )}
      {list.data && all.length >= list.data.limit && <p className="hint">You have {list.data.limit} saved things, the most you can keep.</p>}
      {!mine && list.data && !manage && all.length > 0 && <p className="hint">The business's owners and admins add and change them.</p>}
      {editing && (
        <AccountSheet
          mine={mine}
          checks={!mine && !!provider?.connected}
          thing={editing === "new" ? undefined : editing}
          onClose={() => setEditing(undefined)}
          onSaved={(problem) => {
            setEditing(undefined);
            setNews(problem ?? "");
            list.reload();
          }}
        />
      )}
      {sure.node}
    </section>
  );
}

interface Form {
  name: string;
  title: string;
  bank: string;
  number: string;
  note: string;
}

/** Save an account, or change one. Changing its bank or number asks for a
 * passkey. A business with a payments account sees the bank's name for it
 * before saving. */
function AccountSheet({
  mine,
  checks,
  thing,
  onClose,
  onSaved,
}: {
  mine: boolean;
  checks: boolean;
  thing?: SavedThing;
  onClose: () => void;
  onSaved: (problem?: string) => void;
}) {
  const base = savedBase(mine);
  const banks = useLoad(() => get<BankList>(mine ? "/v1/me/banks" : "/v1/banks"), [mine]);
  const [f, setF] = useState<Form>(() => ({
    name: thing ? `@${thing.name}` : "",
    title: thing?.title ?? "",
    bank: "",
    number: "",
    note: thing?.details.note ?? "",
  }));
  // The saved bank, found in the list once it loads.
  useEffect(() => {
    if (thing && banks.data && !f.bank) setF((cur) => ({ ...cur, bank: bankValueOf(banks.data, thing.details) }));
  }, [thing, banks.data, f.bank]);
  const [problem, setProblem] = useState<string>();
  const [bankName, setBankName] = useState<string>();
  const act = useAction();
  const set = (patch: Partial<Form>) => {
    setF((cur) => ({ ...cur, ...patch }));
    if ("bank" in patch || "number" in patch) setBankName(undefined);
  };
  const bank = bankFields(banks.data, f.bank);
  const wasBank = thing && banks.data ? bankValueOf(banks.data, thing.details) : "";
  const accountChanges = !thing || cleanNumber(f.number) !== "" || (f.bank !== "" && f.bank !== wasBank);

  // The bank's name, asked once a bank and 10 digits are there.
  const asked = useRef("");
  useEffect(() => {
    const n = cleanNumber(f.number);
    if (!checks || !bank || numberProblem(n)) return;
    const key = `${f.bank}/${n}`;
    if (asked.current === key) return;
    asked.current = key;
    post<{ account_name: string }>("/v1/name-check", { ...bank, account_number: n }).then(
      (r) => asked.current === key && setBankName(r.account_name),
      (e: unknown) => asked.current === key && setBankName(e instanceof ApiError && e.status === 400 ? "" : undefined),
    );
  }, [checks, bank, f.bank, f.number]);

  const save = () => {
    const p =
      nameProblem(f.name) ??
      (f.title.trim() ? null : "Give it a title, like Mum or Bola Stores.") ??
      (!thing && !bank ? "Choose the bank." : null) ??
      (!thing || f.number ? numberProblem(f.number) : null);
    setProblem(p ?? undefined);
    if (p) return;
    void act
      .run(async () => {
        const body: Record<string, unknown> = { name: cleanName(f.name), title: f.title.trim(), note: f.note.trim() };
        if (accountChanges) {
          if (bank) Object.assign(body, bank);
          if (f.number) body.account_number = cleanNumber(f.number);
        }
        let out: { check_problem?: string };
        if (thing) {
          if (accountChanges) body.passkey = await assert(`${base}/step-up/options`, { operation: "saved.change", target: thing.id });
          out = await put(`${base}/${encodeURIComponent(thing.id)}`, body);
        } else {
          out = await post(base, { ...body, kind: "bank_account" });
        }
        onSaved(out.check_problem);
      }, thing ? "Saved account changed" : "Account saved")
      .then(() => undefined);
  };
  const taken = act.error instanceof ApiError && act.error.body.name_taken ? "That name is taken: choose another." : undefined;
  return (
    <Modal title={thing ? `Change @${thing.name}` : "Save an account"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
        data-testid="saved-form"
      >
        <Field label="Name to call it up by" hint="Such as @mum or @bayo.">
          <input value={f.name} maxLength={33} autoCapitalize="none" autoComplete="off" onChange={(e) => set({ name: e.target.value })} required />
        </Field>
        <Field label="Title" hint="How it is shown, such as Bayo (rice).">
          <input value={f.title} maxLength={80} onChange={(e) => set({ title: e.target.value })} required />
        </Field>
        <Field label="Bank">
          <select value={f.bank} onChange={(e) => set({ bank: e.target.value })} disabled={!banks.data} data-testid="saved-bank">
            <option value="">{banks.data ? "Choose the bank" : "Loading the banks…"}</option>
            {banks.data?.banks.map((b) => (
              <option key={bankValue(b)} value={bankValue(b)}>
                {b.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Account number" hint={thing ? `Leave it empty to keep ${thing.details.account_number ?? "the number"}.` : "10 digits."}>
          <input value={f.number} inputMode="numeric" autoComplete="off" maxLength={14} onChange={(e) => set({ number: e.target.value })} data-testid="saved-number-input" />
        </Field>
        {bankName && (
          <p className="notice" role="status" data-testid="saved-bank-name">
            The bank's name for it: <strong>{bankName}</strong>
          </p>
        )}
        {bankName === "" && (
          <p className="error" role="alert">
            The bank has no account with that number. Check the number and the bank.
          </p>
        )}
        <Field label="Note" hint="Optional, for your own reference.">
          <input value={f.note} maxLength={200} onChange={(e) => set({ note: e.target.value })} />
        </Field>
        {thing && accountChanges && <p className="hint">Changing the account asks for your passkey.</p>}
        {(problem || taken) && (
          <p className="error" role="alert">
            {problem ?? taken}
          </p>
        )}
        {!taken && <ErrorBox error={act.error ?? banks.error} />}
        <div className="row-actions">
          <button type="submit" className="primary" disabled={act.busy} data-testid="saved-save">
            {act.busy ? "Saving…" : thing ? "Save changes" : "Save account"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
