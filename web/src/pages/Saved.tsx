import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, del, get, post, put } from "../api";
import { Icon } from "../icons";
import { useAuth } from "../auth";
import { assert } from "../passkeys";
import { EmptyState, ErrorBox, Field, Items, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { useConfirm } from "./ConfirmSheet";
import { CustomSheet, FileSheet, KindSheet } from "./SavedKinds";
import {
  BANK_SEARCH_FROM,
  bankFields,
  bankValue,
  bankValueOf,
  checkLine,
  cleanName,
  cleanNumber,
  filterBanks,
  detailsProblem,
  KIND_FIELDS,
  KIND_WORDS,
  kindChoice,
  PROVIDER_TYPES,
  SAVED_KINDS,
  nameProblem,
  numberProblem,
  providerLine,
  savedBase,
  savedKindsBase,
  savedFileURL,
  savedSub,
  shownValue,
  type KindList,
  type MadeKind,
  showConnectStep,
  type BankList,
  type SavedCard,
  type SavedDetails,
  type SavedKind,
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
  const [editing, setEditing] = useState<SavedThing | SavedKind | "choose">();
  // A filter: all, a kind Taskiem knows, or one of the owner's (k:<id>).
  const [kind, setKind] = useState<string>("");
  const [shown, setShown] = useState<Record<string, string>>({});
  const [shownValues, setShownValues] = useState<Record<string, Record<string, string>>>({});
  // Kinds the owner made (saved objects S1, part 5).
  const kinds = useLoad(() => get<KindList>(savedKindsBase(mine)), [mine]);
  const made = kinds.data?.kinds ?? [];
  const [thingOf, setThingOf] = useState<{ kind: MadeKind; thing?: SavedThing }>();
  const [kindEditing, setKindEditing] = useState<MadeKind | "new">();
  const [news, setNews] = useState("");
  const act = useAction();
  const sure = useConfirm();
  const every = list.data?.saved ?? [];
  const all = kind ? every.filter((x) => (kind.startsWith("k:") ? x.kind_id === kind.slice(2) : x.kind === kind)) : every;
  const kindsHeld = SAVED_KINDS.filter((k) => every.some((x) => x.kind === k));
  const madeHeld = made.filter((k) => every.some((x) => x.kind_id === k.id));
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
      const r = await post<{ account_number?: string; values?: Record<string, string> }>(`${base}/${encodeURIComponent(x.id)}/reveal`, { passkey });
      if (r.values) setShownValues((s) => ({ ...s, [x.id]: r.values ?? {} }));
      else setShown((s) => ({ ...s, [x.id]: r.account_number ?? "" }));
    }, false);
  const removeKind = async (k: MadeKind) => {
    const yes = await sure.confirm({ title: `Remove the kind ${k.name}?`, body: "Things of it are not removed with it: it is only removed once none are left.", ok: "Remove", danger: true });
    if (!yes) return;
    await act.run(async () => {
      await del(`${savedKindsBase(mine)}/${encodeURIComponent(k.id)}`);
      kinds.reload();
    }, `${k.name} removed`);
  };

  const title = mine ? "Your saved things" : "Saved things";
  const line = providerLine(provider);
  return (
    <section className="card" aria-labelledby={`saved-h-${mine ? "mine" : "biz"}`} data-testid={mine ? "my-saved" : "saved"}>
      <div className="toolbar">
        <h2 id={`saved-h-${mine ? "mine" : "biz"}`} className="grow" style={{ margin: 0 }}>
          {title}
        </h2>
        {list.data && manage && every.length < list.data.limit && (
          <button type="button" className="small" onClick={() => setEditing("choose")} data-testid="saved-add">
            <Icon name="plus" /> Save something
          </button>
        )}
      </div>
      <p className="hint">
        {mine
          ? "Accounts, people, places and texts of your own, like @mum. Only you see them: no business you work in sees this list."
          : "Accounts, people, places and texts your business uses, called up by name, like @bayo. Everyone in the business sees them, account numbers masked."}
      </p>
      {kindsHeld.length + madeHeld.length > 1 && (
        <div className="segmented" role="group" aria-label="Kinds" data-testid="saved-kinds">
          <button type="button" aria-pressed={kind === ""} onClick={() => setKind("")}>
            All
          </button>
          {kindsHeld.map((k) => (
            <button key={k} type="button" aria-pressed={kind === k} onClick={() => setKind(k)}>
              {KIND_WORDS[k].many}
            </button>
          ))}
          {madeHeld.map((k) => (
            <button key={k.id} type="button" aria-pressed={kind === `k:${k.id}`} onClick={() => setKind(`k:${k.id}`)}>
              {k.name}
            </button>
          ))}
        </div>
      )}
      {line && <p className="hint">{line}</p>}
      {news && (
        <div className="notice" role="status">
          {news}
        </div>
      )}
      <ErrorBox error={list.error ?? act.error} />
      {!list.data && !list.error && <Skeleton rows={2} label="Loading the saved accounts…" />}
      {list.data && every.length === 0 && <EmptyState icon="money">Nothing saved yet.</EmptyState>}
      {all.length > 0 && (
        <Items label={title}>
          {all.map((x) => {
            const bank = x.kind === "bank_account";
            const c = checkLine(x);
            const def = x.kind === "custom" ? made.find((k) => k.id === x.kind_id) : undefined;
            const hidden = !!def && def.fields.some((f) => f.sensitive && x.values?.[f.key ?? ""]);
            const open = shownValues[x.id];
            return (
              <Row
                key={x.id}
                icon={x.kind === "custom" ? "box" : KIND_WORDS[x.kind].icon}
                tone={bank && c.tone === "warn" ? "warn" : undefined}
                title={x.title}
                side={bank ? <span className={`badge sf-status ${c.tone}`}>{c.tone === "ok" ? "Checked" : "Not checked"}</span> : undefined}
                sub={
                  <span className="sub">
                    {savedSub(x)}
                    {bank && (
                      <span className="block" data-testid="saved-check">
                        {c.words}
                      </span>
                    )}
                    {shown[x.id] && (
                      <span className="block" data-testid="saved-number">
                        Account number: <strong>{shown[x.id]}</strong>
                      </span>
                    )}
                    {def && (
                      <span className="block" data-testid="saved-values">
                        {def.fields
                          .filter((f) => x.values?.[f.key ?? ""])
                          .map((f) => `${f.label}: ${open?.[f.key ?? ""] ?? shownValue(f, x.values?.[f.key ?? ""] ?? "")}`)
                          .join(" · ")}
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
                    <button
                      type="button"
                      className="small"
                      onClick={() => (x.kind === "custom" ? def && setThingOf({ kind: def, thing: x }) : setEditing(x))}
                    >
                      Change<span className="sr-only"> @{x.name}</span>
                    </button>
                    {x.kind === "file" && (
                      <a className="button small" href={savedFileURL(x.owner, x.id)} target="_blank" rel="noreferrer">
                        Open<span className="sr-only"> @{x.name}</span>
                      </a>
                    )}
                    {hidden && !open && (
                      <button type="button" className="small" disabled={act.busy} onClick={() => reveal(x)}>
                        Show hidden<span className="sr-only"> of @{x.name}</span>
                      </button>
                    )}
                    {bank && !shown[x.id] && (
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
      {list.data && every.length >= list.data.limit && <p className="hint">You have {list.data.limit} saved things, the most you can keep.</p>}
      {!mine && list.data && !manage && every.length > 0 && <p className="hint">The business's owners and admins add and change them.</p>}
      {kinds.data && (made.length > 0 || manage) && (
        <section aria-labelledby={`kinds-h-${mine ? "mine" : "biz"}`} data-testid="saved-made-kinds">
          <div className="toolbar">
            <h3 id={`kinds-h-${mine ? "mine" : "biz"}`} className="grow" style={{ margin: 0 }}>
              {mine ? "Your own kinds" : "The business's own kinds"}
            </h3>
            {manage && made.length < kinds.data.limit && (
              <button type="button" className="small" onClick={() => setKindEditing("new")} data-testid="kind-make">
                <Icon name="plus" /> Make a kind
              </button>
            )}
          </div>
          {made.length === 0 ? (
            <p className="hint">When none of the kinds above fits, make one: a car, a generator, a tenant, with the fields you need.</p>
          ) : (
            <Items label={mine ? "Your own kinds" : "The business's own kinds"}>
              {made.map((k) => (
                <Row key={k.id} icon="box" title={k.name} sub={`${k.fields.length} ${k.fields.length === 1 ? "field" : "fields"} · ${k.count} saved`} testid="kind-row">
                  {manage && (
                    <>
                      <button type="button" className="small" onClick={() => setKindEditing(k)}>
                        Change<span className="sr-only"> {k.name}</span>
                      </button>
                      <button type="button" className="small danger" disabled={act.busy} onClick={() => void removeKind(k)}>
                        Remove<span className="sr-only"> {k.name}</span>
                      </button>
                    </>
                  )}
                </Row>
              ))}
            </Items>
          )}
        </section>
      )}
      {kindEditing && kinds.data && (
        <KindSheet
          mine={mine}
          kind={kindEditing === "new" ? undefined : kindEditing}
          starters={kinds.data.starters.filter((st) => !made.some((k) => k.name.toLowerCase() === st.name.toLowerCase()))}
          onClose={() => setKindEditing(undefined)}
          onSaved={() => {
            setKindEditing(undefined);
            kinds.reload();
            list.reload();
          }}
        />
      )}
      {thingOf && (
        <CustomSheet
          mine={mine}
          kind={thingOf.kind}
          thing={thingOf.thing}
          onClose={() => setThingOf(undefined)}
          onSaved={() => {
            setThingOf(undefined);
            list.reload();
            kinds.reload();
          }}
        />
      )}
      {editing === "choose" && (
        <Modal title="Save something" onClose={() => setEditing(undefined)}>
          <Items label="What to save">
            {made.map((k) => (
              <Row key={k.id} icon="box" title={`A ${k.name.toLowerCase()}`} sub="One of your own kinds" testid="saved-kind">
                <button
                  type="button"
                  className="small"
                  onClick={() => {
                    setEditing(undefined);
                    setThingOf({ kind: k });
                  }}
                >
                  Choose<span className="sr-only"> a {k.name.toLowerCase()}</span>
                </button>
              </Row>
            ))}
            {SAVED_KINDS.map((k) => (
              <Row key={k} icon={KIND_WORDS[k].icon} title={kindChoice(k)} testid="saved-kind">
                <button type="button" className="small" onClick={() => setEditing(k)}>
                  Choose<span className="sr-only"> {KIND_WORDS[k].one}</span>
                </button>
              </Row>
            ))}
          </Items>
        </Modal>
      )}
      {editing && editing !== "choose" && (editing === "bank_account" || (typeof editing === "object" && editing.kind === "bank_account")) && (
        <AccountSheet
          mine={mine}
          provider={mine ? undefined : provider}
          checks={!mine && !!provider?.connected}
          thing={typeof editing === "object" ? editing : undefined}
          onClose={() => setEditing(undefined)}
          onSaved={(problem) => {
            setEditing(undefined);
            setNews(problem ?? "");
            list.reload();
          }}
        />
      )}
      {(editing === "file" || (typeof editing === "object" && editing.kind === "file")) && (
        <FileSheet
          mine={mine}
          thing={typeof editing === "object" ? editing : undefined}
          onClose={() => setEditing(undefined)}
          onSaved={() => {
            setEditing(undefined);
            list.reload();
          }}
        />
      )}
      {editing && editing !== "choose" && editing !== "bank_account" && editing !== "file" && (typeof editing !== "object" || (editing.kind !== "bank_account" && editing.kind !== "file")) && (
        <ThingSheet
          mine={mine}
          kind={typeof editing === "object" ? (editing.kind as Exclude<SavedKind, "bank_account">) : (editing as Exclude<SavedKind, "bank_account">)}
          thing={typeof editing === "object" ? editing : undefined}
          onClose={() => setEditing(undefined)}
          onSaved={() => {
            setEditing(undefined);
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
export function AccountSheet({
  mine,
  provider,
  checks,
  thing,
  start,
  onClose,
  onSaved,
}: {
  mine: boolean;
  provider?: SavedProvider;
  checks: boolean;
  thing?: SavedThing;
  /** A shared card to save (Save this): its fields to begin with. */
  start?: SavedCard;
  onClose: () => void;
  onSaved: (problem?: string) => void;
}) {
  const base = savedBase(mine);
  const banks = useLoad(() => get<BankList>(mine ? "/v1/me/banks" : "/v1/banks"), [mine]);
  const [f, setF] = useState<Form>(() => ({
    name: thing ? `@${thing.name}` : start?.name ? `@${start.name}` : "",
    title: thing?.title ?? start?.title ?? "",
    bank: "",
    number: start?.details.account_number ?? "",
    note: thing?.details.note ?? "",
  }));
  // The saved (or shared) bank, found in the list once it loads.
  const from = thing?.details ?? start?.details;
  useEffect(() => {
    if (from && banks.data && !f.bank) setF((cur) => ({ ...cur, bank: bankValueOf(banks.data, from) }));
  }, [from, banks.data, f.bank]);
  const [problem, setProblem] = useState<string>();
  const [bankName, setBankName] = useState<string>();
  const [bankSearch, setBankSearch] = useState("");
  const shownBanks = filterBanks(banks.data?.banks ?? [], bankSearch, f.bank);
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
        {(banks.data?.banks.length ?? 0) > BANK_SEARCH_FROM && (
          <Field label="Find the bank" hint="Type part of its name, such as guaranty or kuda.">
            <input type="search" value={bankSearch} autoComplete="off" onChange={(e) => setBankSearch(e.target.value)} data-testid="saved-bank-search" />
          </Field>
        )}
        <Field label="Bank">
          <select value={f.bank} onChange={(e) => set({ bank: e.target.value })} disabled={!banks.data} data-testid="saved-bank">
            <option value="">{!banks.data ? "Loading the banks…" : shownBanks.length === 0 ? "No bank has that name" : "Choose the bank"}</option>
            {shownBanks.map((b) => (
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
        {provider && !provider.connected && (
          <div className="warning" role="note" data-testid="saved-form-connect">
            The bank's name is not checked until your business connects a payments account; it is saved as "Name not checked yet".
            {provider.owner && provider.wallet?.available && provider.wallet.plan && (
              <>
                {" "}
                <Link to="/settings/payments">Open a Taskiem wallet</Link>
              </>
            )}
          </div>
        )}
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

/** Save a person, a place, a text or an account with a provider, or
 * change one: its name, title, note and the kind's own fields. No passkey:
 * none of these moves money. */
export function ThingSheet({
  mine,
  kind,
  thing,
  start,
  onClose,
  onSaved,
}: {
  mine: boolean;
  kind: Exclude<SavedKind, "bank_account">;
  thing?: SavedThing;
  /** A shared card to save (Save this): its fields to begin with. */
  start?: SavedCard;
  onClose: () => void;
  onSaved: () => void;
}) {
  const base = savedBase(mine);
  const [name, setName] = useState(thing ? `@${thing.name}` : start?.name ? `@${start.name}` : "");
  const [title, setTitle] = useState(thing?.title ?? start?.title ?? "");
  const [d, setD] = useState<SavedDetails>(() => ({ ...(thing?.details ?? start?.details ?? {}) }));
  const [problem, setProblem] = useState<string>();
  const act = useAction();
  const fields = KIND_FIELDS[kind];
  const save = () => {
    const p = nameProblem(name) ?? (title.trim() ? null : "Give it a title, like Mum or Our warehouse.") ?? detailsProblem(kind, d);
    setProblem(p ?? undefined);
    if (p) return;
    // Each field: its words, or (changing one) null to remove it.
    const details: Record<string, string | null> = {};
    for (const f of [...fields.map((x) => x.key), "note" as const, ...(kind === "provider_account" ? ["type" as const] : [])]) {
      const v = String(d[f] ?? "").trim();
      if (v) details[f] = f === "body" ? String(d[f]) : v;
      else if (thing) details[f] = null;
    }
    void act
      .run(async () => {
        const body = { name: cleanName(name), title: title.trim(), details };
        if (thing) await put(`${base}/${encodeURIComponent(thing.id)}`, body);
        else await post(base, { ...body, kind });
        onSaved();
      }, thing ? "Saved" : `Saved as @${cleanName(name)}`)
      .then(() => undefined);
  };
  const taken = act.error instanceof ApiError && act.error.body.name_taken ? "That name is taken: choose another." : undefined;
  const one = KIND_WORDS[kind].one;
  return (
    <Modal title={thing ? `Change @${thing.name}` : `Save ${one}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
        data-testid="saved-thing-form"
      >
        <Field label="Name to call it up by" hint={kind === "text" ? "Such as @bank-details or @price-list." : "Such as @mum or @warehouse."}>
          <input value={name} maxLength={33} autoCapitalize="none" autoComplete="off" onChange={(e) => setName(e.target.value)} required />
        </Field>
        <Field label="Title" hint="How it is shown.">
          <input value={title} maxLength={80} onChange={(e) => setTitle(e.target.value)} required />
        </Field>
        {kind === "provider_account" && (
          <Field label="What the provider is">
            <select value={d.type ?? ""} onChange={(e) => setD((cur) => ({ ...cur, type: (e.target.value || undefined) as SavedDetails["type"] }))}>
              <option value="">Choose</option>
              {PROVIDER_TYPES.map((p) => (
                <option key={p.value} value={p.value}>
                  {p.label}
                </option>
              ))}
            </select>
          </Field>
        )}
        {fields.map((f) => (
          <Field key={f.key} label={f.label} hint={f.hint}>
            {f.long ? (
              <textarea value={String(d[f.key] ?? "")} rows={6} maxLength={4000} onChange={(e) => setD((cur) => ({ ...cur, [f.key]: e.target.value }))} />
            ) : (
              <input
                value={String(d[f.key] ?? "")}
                type={f.key === "email" ? "email" : f.key === "birthday" ? "date" : f.key === "phone" ? "tel" : "text"}
                inputMode={f.key === "phone" ? "tel" : undefined}
                maxLength={200}
                onChange={(e) => setD((cur) => ({ ...cur, [f.key]: e.target.value }))}
              />
            )}
          </Field>
        ))}
        <Field label="Note" hint="Optional, for your own reference.">
          <input value={d.note ?? ""} maxLength={200} onChange={(e) => setD((cur) => ({ ...cur, note: e.target.value }))} />
        </Field>
        {(problem || taken) && (
          <p className="error" role="alert">
            {problem ?? taken}
          </p>
        )}
        {!taken && <ErrorBox error={act.error} />}
        <div className="row-actions">
          <button type="submit" className="primary" disabled={act.busy} data-testid="saved-thing-save">
            {act.busy ? "Saving…" : "Save"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
