// Pay several people (P3; docs/spec/chat-pay-people.md, "Pay several
// people"): the list filled by picking saved accounts, riders and staff or by
// pasting a list (each line read, those that cannot be read shown to
// fix); then checked (POST /v1/payment-batches): each row prepared as one
// payment is, the rows failing a check held out in amber with why; then
// confirmed with one passkey for the batch (operation money.pay.batch,
// target batch:<id>), and a row per person filling in as each goes, with
// paying the failed ones again and, for owners, one OK for all that wait.

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  Link,
  useLocation,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { ApiError, get, post } from "../api";
import { useAuth } from "../auth";
import { passkeysSupported, stepUpAssert } from "../passkeys";
import {
  EmptyState,
  ErrorBox,
  Field,
  Item,
  Items,
  Modal,
  PageHeader,
  Row,
  Skeleton,
  fmtTime,
  useAction,
  useLoad,
} from "../ui";
import { naira } from "../lib/invoices";
import { PhotoReadButton, readingPhoto, readPhotoWords } from "./PhotoRead";
import type { PayLine } from "../lib/dispatch";
import type { SavedList, SavedThing } from "../lib/saved";
import {
  payAccountWords,
  staffUnpayable,
  type StaffPayAccount,
} from "../lib/team";
import {
  payeeName,
  paymentSub,
  payWarnings,
  type Payment,
} from "../lib/payments";
import {
  FOR_CHOICES,
  MAX_ROWS,
  addRows,
  againIds,
  batchCounts,
  batchName,
  batchTag,
  confirmWords,
  draftTotal,
  footWords,
  heldOnto,
  listTag,
  listWords,
  peopleWords,
  pollAfter,
  preparedIds,
  progressWords,
  resultsBy,
  rowBody,
  rowFromLine,
  rowFromRider,
  rowFromSaved,
  rowFromStaff,
  rowProblem,
  rowStatus,
  waitingIds,
  type BatchHeld,
  type BatchLine,
  type DraftRow,
  type PaymentBatch,
} from "../lib/batches";

/** What the list page hands the batch's page, and back again to change it. */
interface ListState {
  rows?: DraftRow[];
  title?: string;
  held?: BatchHeld[];
}

const listKind = (rows: DraftRow[]) =>
  rows.length > 0 && rows.every((r) => r.kind === "rider") ? "riders" : "list";

/** Makes a batch (from rows or as a copy of one) and opens it. */
function useMakeBatch() {
  const navigate = useNavigate();
  const act = useAction();
  const make = (body: Record<string, unknown>, keep: ListState = {}) =>
    act.run(async () => {
      const r = await post<{ batch: PaymentBatch }>("/v1/payment-batches", {
        from: "tools",
        ...body,
      });
      navigate(`/payments/batches/${encodeURIComponent(r.batch.id)}`, {
        state: { ...keep, held: r.batch.held ?? [] } satisfies ListState,
      });
    }, false);
  return { make, busy: act.busy, error: act.error };
}

/** /payments/several: the list being filled. ?riders=1 starts it with
 * what is due to each rider. */
export function PayListPage() {
  const location = useLocation();
  const back = (location.state ?? {}) as ListState;
  const [params] = useSearchParams();
  const riders = params.get("riders") === "1";
  const [rows, setRows] = useState<DraftRow[]>(back.rows ?? []);
  const [title, setTitle] = useState(
    back.title ?? (riders ? "Riders' pay" : ""),
  );
  const [adding, setAdding] = useState<
    "saved" | "riders" | "staff" | "paste"
  >();
  const [tried, setTried] = useState(false);
  const [news, setNews] = useState<string>();
  // From a photo of a list: its words, read on this device, put in the
  // paste box and read as a pasted list is (from: "photo").
  const [photoWords, setPhotoWords] = useState<string>();
  const [photoBusy, setPhotoBusy] = useState(false);
  const fromPhoto = async (photo: File) => {
    if (readingPhoto()) return;
    setPhotoBusy(true);
    try {
      const words = await readPhotoWords(photo);
      if (!words) return;
      setPhotoWords(words);
      setAdding("paste");
    } finally {
      setPhotoBusy(false);
    }
  };
  const batch = useMakeBatch();
  const { can } = useAuth();
  // From Riders > Pay: each rider with a bank account and something due.
  const due = useLoad(
    () =>
      riders && !back.rows
        ? get<{ lines: PayLine[] }>("/v1/riders/pay")
        : Promise.resolve(undefined),
    [riders],
  );
  useEffect(() => {
    const lines = due.data?.lines;
    if (!lines) return;
    setRows(
      (cur) =>
        addRows(
          cur,
          lines.filter((l) => l.bank && l.net_kobo > 0).map(rowFromRider),
        ).rows,
    );
  }, [due.data]);

  const add = (more: DraftRow[]) => {
    const r = addRows(rows, more);
    setRows(r.rows);
    const added = r.rows.length - rows.length;
    setNews(
      r.left > 0
        ? `Added ${peopleWords(added)}. Up to ${MAX_ROWS} people a list: ${peopleWords(r.left)} left out.`
        : `Added ${peopleWords(added)}.`,
    );
    setAdding(undefined);
  };
  const change = (key: string, patch: Partial<DraftRow>) =>
    setRows((cur) =>
      cur.map((r) =>
        r.key === key ? { ...r, ...patch, problem: undefined } : r,
      ),
    );
  const problems = rows.map(rowProblem);
  const ready = rows.length > 0 && problems.every((p) => p === null);
  const check = () => {
    setTried(true);
    if (!ready || batch.busy) return;
    void batch.make(
      { title: title.trim(), kind: listKind(rows), rows: rows.map(rowBody) },
      { rows, title },
    );
  };
  return (
    <>
      <p className="crumbs">
        <Link to="/payments" aria-label="Back to Pay people">
          Pay people
        </Link>
      </p>
      <PageHeader
        title="Pay several people"
        description="Pick saved accounts, riders or staff, or paste a list. Each person is checked with the bank; you confirm them all with one passkey."
      />
      <section
        className="card"
        aria-labelledby="list-who-h"
        data-testid="pay-list"
      >
        <h2 id="list-who-h">Who to pay</h2>
        <div className="toolbar">
          <button
            type="button"
            onClick={() => setAdding("saved")}
            data-testid="add-saved"
          >
            Add saved accounts
          </button>
          {can("money.pay") && (
            <button
              type="button"
              onClick={() => setAdding("riders")}
              data-testid="add-riders"
            >
              Add riders
            </button>
          )}
          {can("money.pay") && (
            <button
              type="button"
              onClick={() => setAdding("staff")}
              data-testid="add-staff"
            >
              Add staff
            </button>
          )}
          <button
            type="button"
            onClick={() => setAdding("paste")}
            data-testid="add-paste"
          >
            Paste a list
          </button>
        </div>
        <PhotoReadButton
          label="From a photo of a list"
          describe="Add people from a photo of a list. Read on this device, not sent."
          busy={photoBusy}
          onPhoto={(p) => void fromPhoto(p)}
          testid="add-photo"
        />
        {news && (
          <div className="notice" role="status">
            {news}
          </div>
        )}
        <ErrorBox error={due.error} />
        {riders && !due.data && !due.error && !back.rows && (
          <Skeleton rows={3} label="Loading what is due to riders…" />
        )}
        <Field
          label="Name this list (optional)"
          hint="Such as Riders, week 45. It helps you find it next time."
        >
          <input
            value={title}
            maxLength={80}
            autoComplete="off"
            onChange={(e) => setTitle(e.target.value)}
            data-testid="list-title"
          />
        </Field>
        {rows.length === 0 ? (
          <EmptyState icon="members">
            Nobody on the list yet. Add saved accounts, riders or staff, or
            paste a list, one person a line.
          </EmptyState>
        ) : (
          <Items label="People to pay">
            {rows.map((r, i) => (
              <DraftLine
                key={r.key}
                r={r}
                n={i + 1}
                problem={r.problem ?? (tried ? problems[i] : null) ?? undefined}
                set={(patch) => change(r.key, patch)}
                remove={() =>
                  setRows((cur) => cur.filter((x) => x.key !== r.key))
                }
              />
            ))}
          </Items>
        )}
      </section>
      <section
        className="card batch-foot"
        aria-label="The list's total"
        data-testid="list-foot"
      >
        <p className="batch-foot-sum">
          <strong data-testid="list-total">
            {footWords(rows.length, draftTotal(rows))}
          </strong>
          <span className="hint">Nothing is paid until you confirm.</span>
        </p>
        {tried && !ready && rows.length > 0 && (
          <p className="error" role="alert">
            Fix the rows marked, or remove them.
          </p>
        )}
        <ErrorBox error={batch.error} />
        <button
          type="button"
          className="primary"
          onClick={check}
          disabled={batch.busy || rows.length === 0}
          data-testid="list-check"
        >
          {batch.busy ? "Checking with the bank…" : "Check the list"}
        </button>
      </section>
      {adding === "saved" && (
        <PickSaved onClose={() => setAdding(undefined)} onAdd={add} />
      )}
      {adding === "riders" && (
        <PickRiders onClose={() => setAdding(undefined)} onAdd={add} />
      )}
      {adding === "staff" && (
        <PickStaff onClose={() => setAdding(undefined)} onAdd={add} />
      )}
      {adding === "paste" && (
        <PasteList
          key={photoWords ?? ""}
          photoWords={photoWords}
          onClose={() => {
            setAdding(undefined);
            setPhotoWords(undefined);
          }}
          onAdd={(r) => {
            setPhotoWords(undefined);
            add(r);
          }}
        />
      )}
    </>
  );
}

/** One row of the list: who, how much and what for; an account number
 * and its bank for a pasted line. */
function DraftLine({
  r,
  n,
  problem,
  set,
  remove,
}: {
  r: DraftRow;
  n: number;
  problem?: string;
  set: (p: Partial<DraftRow>) => void;
  remove: () => void;
}) {
  const who = r.who || `Row ${n}`;
  return (
    <Row
      letter={who}
      tone={problem ? "warn" : undefined}
      title={who}
      testid="list-row"
      attrs={problem ? { "data-problem": "yes" } : undefined}
      sub={
        <span className="sub">
          {r.sub && <span className="block">{r.sub}</span>}
          {problem && (
            <span
              className="block pay-warn warn batch-problem"
              data-testid="list-row-problem"
            >
              {problem}
            </span>
          )}
          <span className="batch-fields">
            {r.kind === "account" && (
              <>
                <Field label="Account number">
                  <input
                    inputMode="numeric"
                    maxLength={13}
                    autoComplete="off"
                    value={r.number ?? ""}
                    onChange={(e) => set({ number: e.target.value })}
                    data-testid="list-number"
                  />
                </Field>
                <Field label="Bank">
                  <input
                    autoComplete="off"
                    value={r.bank ?? ""}
                    onChange={(e) => set({ bank: e.target.value })}
                    data-testid="list-bank"
                  />
                </Field>
              </>
            )}
            <Field label="How much (₦)">
              <input
                inputMode="decimal"
                autoComplete="off"
                value={r.amount}
                onChange={(e) => set({ amount: e.target.value })}
                data-testid="list-amount"
              />
            </Field>
            <Field label="What for">
              <select
                value={r.for}
                onChange={(e) => set({ for: e.target.value })}
                data-testid="list-for"
              >
                <option value="">Choose</option>
                {FOR_CHOICES.map((c) => (
                  <option key={c} value={c}>
                    {c.charAt(0).toUpperCase() + c.slice(1)}
                  </option>
                ))}
              </select>
            </Field>
          </span>
        </span>
      }
    >
      <button type="button" className="small" onClick={remove}>
        Remove<span className="sr-only"> {who}</span>
      </button>
    </Row>
  );
}

/** Ticks in a list of things to add, and the button that adds them. */
function Picker<T>({
  title,
  items,
  loading,
  error,
  empty,
  label,
  sub,
  disabled,
  onClose,
  onAdd,
}: {
  title: string;
  items: T[] | undefined;
  loading: string;
  error: unknown;
  empty: ReactNode;
  label: (x: T) => string;
  sub: (x: T) => string;
  disabled?: (x: T) => string | null;
  onClose: () => void;
  onAdd: (picked: T[]) => void;
}) {
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const open = (items ?? [])
    .map((x, i) => [x, i] as const)
    .filter(([x]) => !disabled?.(x));
  const all = open.length > 0 && open.every(([, i]) => picked.has(i));
  return (
    <Modal title={title} onClose={onClose}>
      <ErrorBox error={error} />
      {!items && !error && <Skeleton rows={3} label={loading} />}
      {items && items.length === 0 && (
        <EmptyState icon="members">{empty}</EmptyState>
      )}
      {items && items.length > 0 && (
        <>
          {open.length > 1 && (
            <label className="switch-line">
              <input
                type="checkbox"
                checked={all}
                onChange={() =>
                  setPicked(all ? new Set() : new Set(open.map(([, i]) => i)))
                }
                data-testid="pick-all"
              />{" "}
              <span>All of them</span>
            </label>
          )}
          <ul className="batch-picks" aria-label={title}>
            {items.map((x, i) => {
              const off = disabled?.(x) ?? null;
              return (
                <li key={i}>
                  <label className="batch-pick">
                    <input
                      type="checkbox"
                      disabled={!!off}
                      checked={picked.has(i)}
                      onChange={() =>
                        setPicked((cur) => {
                          const next = new Set(cur);
                          if (next.has(i)) next.delete(i);
                          else next.add(i);
                          return next;
                        })
                      }
                      data-testid="pick"
                    />{" "}
                    <span>
                      {label(x)}
                      <span className="block sub">{off ?? sub(x)}</span>
                    </span>
                  </label>
                </li>
              );
            })}
          </ul>
        </>
      )}
      <div className="row-actions">
        <button
          type="button"
          className="primary"
          disabled={picked.size === 0}
          onClick={() => onAdd((items ?? []).filter((_, i) => picked.has(i)))}
          data-testid="pick-add"
        >
          {picked.size ? `Add ${peopleWords(picked.size)}` : "Add"}
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

function PickSaved({
  onClose,
  onAdd,
}: {
  onClose: () => void;
  onAdd: (rows: DraftRow[]) => void;
}) {
  const list = useLoad(() => get<SavedList>("/v1/saved?kind=bank_account"), []);
  const items = list.data?.saved.filter((x) => x.kind === "bank_account");
  return (
    <Picker<SavedThing>
      title="Add saved accounts"
      items={items}
      loading="Loading the saved accounts…"
      error={list.error}
      empty="No bank accounts saved yet. Save one under Saved, or paste a list."
      label={(x) => x.check?.account_name || x.title || `@${x.name}`}
      sub={(x) => (x.hint ? `@${x.name} · ${x.hint}` : `@${x.name}`)}
      onClose={onClose}
      onAdd={(picked) => onAdd(picked.map(rowFromSaved))}
    />
  );
}

function PickRiders({
  onClose,
  onAdd,
}: {
  onClose: () => void;
  onAdd: (rows: DraftRow[]) => void;
}) {
  const pay = useLoad(() => get<{ lines: PayLine[] }>("/v1/riders/pay"), []);
  return (
    <Picker<PayLine>
      title="Add riders"
      items={pay.data?.lines}
      loading="Loading what is due to riders…"
      error={pay.error}
      empty="Nothing is due to riders: their fees show here once they deliver."
      label={(l) => l.rider}
      sub={(l) => `${naira(Math.max(l.net_kobo, 0))} due · ${l.bank}`}
      disabled={(l) =>
        l.bank ? null : "No bank account kept: add one under Riders > Pay"
      }
      onClose={onClose}
      onAdd={(picked) => onAdd(picked.map(rowFromRider))}
    />
  );
}

/** Staff, each with the account linked to them (GET /v1/payments/staff,
 * for anyone who may pay): those with one can be added, for wages; those
 * without are shown with why. */
/** A staff member as the payers' list gives them. */
type PayStaff = {
  user_id: string;
  name: string;
  pay_account: StaffPayAccount | null;
};

function PickStaff({
  onClose,
  onAdd,
}: {
  onClose: () => void;
  onAdd: (rows: DraftRow[]) => void;
}) {
  const { me } = useAuth();
  const owner = !!me?.roles.includes("owner");
  const people = useLoad(
    async () => (await get<{ staff: PayStaff[] }>("/v1/payments/staff")).staff,
    [],
  );
  return (
    <Picker<PayStaff>
      title="Add staff"
      items={people.data}
      loading="Loading your staff…"
      error={people.error}
      empty="No staff yet. Add them under Staff."
      label={(p) => p.name || "A staff member"}
      sub={(p) => (p.pay_account ? payAccountWords(p.pay_account) : "")}
      disabled={(p) => staffUnpayable(p, owner)}
      onClose={onClose}
      onAdd={(picked) =>
        onAdd(
          picked.flatMap((p) =>
            p.pay_account
              ? [rowFromStaff({ ...p, pay_account: p.pay_account })]
              : [],
          ),
        )
      }
    />
  );
}

/** A pasted list, one person a line ("Chidi 0123456789 GTBank 45k"), read
 * by the server; each line becomes a row, those that cannot be read with
 * why, to fix in the list. */
function PasteList({
  onClose,
  onAdd,
  photoWords,
}: {
  onClose: () => void;
  onAdd: (rows: DraftRow[]) => void;
  /** The words of a photo of a list, read on this device: put in the box
   * and read at once, with from: "photo" while the box holds them. */
  photoWords?: string;
}) {
  const [text, setText] = useState(photoWords ?? "");
  const act = useAction();
  const lines = text.split("\n").filter((l) => l.trim()).length;
  const fromPhoto = !!photoWords && !!text.trim();
  const read = (words = text) =>
    act.run(async () => {
      const r = await post<{ lines: BatchLine[] }>("/v1/payment-batches/read", {
        text: words,
        ...(fromPhoto ? { from: "photo" } : {}),
      });
      onAdd(r.lines.map(rowFromLine));
    }, false);
  // A photo's words: read as soon as they are in the box; what cannot be
  // read is shown in the list to fix, as a pasted line's is.
  const started = useRef(false);
  useEffect(() => {
    if (!photoWords || started.current) return;
    started.current = true;
    if (photoWords.split("\n").filter((l) => l.trim()).length <= MAX_ROWS)
      void read(photoWords);
  }, [photoWords]);
  return (
    <Modal title={photoWords ? "A photo of a list" : "Paste a list"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void read();
        }}
      >
        <Field
          label="One person a line"
          hint="Their name, account number, bank and how much, such as Chidi 0123456789 GTBank 45k."
        >
          <textarea
            rows={6}
            value={text}
            onChange={(e) => setText(e.target.value)}
            data-testid="paste-text"
          />
        </Field>
        {lines > MAX_ROWS && (
          <p className="error" role="alert">
            Up to {MAX_ROWS} people a list.
          </p>
        )}
        <ErrorBox error={act.error} />
        <div className="row-actions">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || lines === 0 || lines > MAX_ROWS}
            data-testid="paste-read"
          >
            {act.busy ? "Reading…" : "Read the list"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/**
 * A batch's confirm or OK, with a passkey asked for this batch alone
 * (batches take no authenticator code): first without a proof so the
 * server says it wants one (403 step_up), then with it. body is read when
 * pressed: the rows the person sees, by id, so nothing else is sent.
 */
function useBatchProof(
  path: string,
  op: string,
  target: string,
  onDone: (b: PaymentBatch) => void,
  onChanged: () => void,
) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [noPasskey, setNoPasskey] = useState<"none" | "browser">();
  const send = async (body: Record<string, unknown>) =>
    onDone((await post<{ batch: PaymentBatch }>(path, body)).batch);
  const go = async (body: Record<string, unknown>) => {
    setBusy(true);
    setError(undefined);
    setNoPasskey(undefined);
    try {
      try {
        await send(body);
      } catch (e) {
        if (!(e instanceof ApiError) || e.status !== 403 || !e.body.step_up)
          throw e;
        const methods = Array.isArray(e.body.methods)
          ? (e.body.methods as string[])
          : [];
        if (!methods.includes("passkey")) return setNoPasskey("none");
        if (!passkeysSupported()) return setNoPasskey("browser");
        const o = typeof e.body.operation === "string" ? e.body.operation : op;
        const t = typeof e.body.target === "string" ? e.body.target : target;
        await send({ ...body, passkey: await stepUpAssert(o, t) });
      }
    } catch (e) {
      // The list changed since it was shown: read again before anything goes.
      if (e instanceof ApiError && e.body.code === "changed") onChanged();
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  const view = (
    <>
      {noPasskey && (
        <div
          className="pay-warn warn"
          role="alert"
          data-testid="batch-needs-passkey"
        >
          {noPasskey === "none"
            ? "Paying a list takes a passkey: add one under Account first."
            : "This browser cannot use your passkey here."}{" "}
          <Link to="/account">Go to Account</Link>
        </div>
      )}
      <ErrorBox error={error} />
    </>
  );
  return { busy, go, view };
}

/** /payments/batches/:id: one list. To confirm, it shows each person
 * prepared and the rows held out; once confirmed, a row per person
 * filling in as each is paid. */
export function BatchPage() {
  const { id = "" } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const passed = (location.state ?? {}) as ListState;
  const { data, error, reload } = useLoad(
    () =>
      get<{ batch: PaymentBatch }>(
        `/v1/payment-batches/${encodeURIComponent(id)}`,
      ),
    [id],
  );
  const [now, setNow] = useState<PaymentBatch>();
  const [results, setResults] = useState<PaymentBatch["results"]>();
  const [dismissed, setDismissed] = useState<Set<number>>(new Set());
  useEffect(() => setNow(undefined), [data]);
  useEffect(() => {
    setResults(undefined);
    setDismissed(new Set());
  }, [id]);
  const b = now && now.id === id ? now : data?.batch;
  // Read again while money is moving or rows wait for the OK.
  const wait = b ? pollAfter(b) : null;
  useEffect(() => {
    if (wait === null) return;
    const t = window.setTimeout(reload, wait);
    return () => window.clearTimeout(t);
  }, [wait, reload, data]);
  const owner = !!useAuth().me?.roles.includes("owner");
  const done = (x: PaymentBatch) => {
    setNow(x);
    setResults(x.results);
    reload();
  };
  const pay = useBatchProof(
    `/v1/payment-batches/${encodeURIComponent(id)}/confirm`,
    "money.pay.batch",
    `batch:${id}`,
    done,
    reload,
  );
  const ok = useBatchProof(
    `/v1/payment-batches/${encodeURIComponent(id)}/approve`,
    "money.pay.approve",
    `batch:${id}`,
    done,
    reload,
  );
  const act = useAction();
  const againAct = useAction();
  const make = useMakeBatch();
  const held = (passed.held ?? []).filter((h) => !dismissed.has(h.row));
  const problems = useMemo(() => resultsBy(results), [results]);
  // Asked "pay again?" (the same payment in the last day): a yes per row.
  const asked = useMemo(() => againIds(results), [results]);
  const [yes, setYes] = useState<Set<string>>(new Set());
  useEffect(() => setYes(new Set()), [results]);
  const tick = (pid: string) => (v: boolean) =>
    setYes((cur) => {
      const n = new Set(cur);
      if (v) n.add(pid);
      else n.delete(pid);
      return n;
    });

  if (!b)
    return (
      <>
        <BatchCrumbs />
        <PageHeader title="Pay several people" />
        <ErrorBox error={error} />
        {!error && <Skeleton rows={4} label="Loading the list…" />}
      </>
    );
  const tag = batchTag(b);
  const c = batchCounts(b);
  const preparing = b.status === "preparing";
  const prepared = (b.status === "prepared" && c.prepared > 0) || preparing;
  const shown = b.payments.filter(
    (x) => x.status !== "cancelled" || b.status === "cancelled",
  );
  const pct =
    b.total_kobo > 0 ? Math.round((b.paid_kobo / b.total_kobo) * 100) : 0;
  const changeList = () =>
    void act.run(async () => {
      await post(`/v1/payment-batches/${encodeURIComponent(b.id)}/cancel`);
      navigate("/payments/several", {
        state: {
          rows: heldOnto(passed.rows ?? [], passed.held),
          title: passed.title,
        } satisfies ListState,
      });
    }, false);
  return (
    <>
      <BatchCrumbs />
      <PageHeader title={batchName(b)} />
      <section className="hero" aria-label="The list" data-testid="batch-hero">
        <div className="hero-top">
          <span className="hero-label">{prepared ? "To pay" : "Paid"}</span>
          <span className="hero-tag" data-testid="batch-status">
            {tag.label}
          </span>
        </div>
        <div className="hero-amount" data-testid="batch-amount">
          {naira(
            prepared
              ? b.payments
                  .filter((x) => x.status === "prepared")
                  .reduce((t, x) => t + x.amount_kobo, 0)
              : b.paid_kobo,
          )}
        </div>
        <p className="hero-sub" data-testid="batch-sub">
          {prepared
            ? `${peopleWords(c.prepared)}, each checked with the bank`
            : `of ${naira(b.total_kobo)} to ${peopleWords(b.count)}`}
        </p>
        {!prepared && b.status !== "cancelled" && (
          <div className="hero-bar" role="img" aria-label={`${pct}% paid`}>
            <span style={{ width: `${pct}%` }} />
          </div>
        )}
        <dl className="hero-foot">
          <div>
            <dt>{b.confirmed_at ? "Confirmed" : "Made"}</dt>
            <dd className="hero-foot-words">
              {fmtTime(b.confirmed_at ?? b.created_at)}
            </dd>
          </div>
          <div>
            <dt>How it stands</dt>
            <dd className="hero-foot-words" data-testid="batch-progress">
              {progressWords(b)}
            </dd>
          </div>
        </dl>
      </section>

      {prepared && (
        <div className="toolbar">
          <button
            type="button"
            className="primary"
            disabled={preparing || pay.busy || act.busy}
            onClick={() =>
              void pay.go({ payments: preparedIds(b), again: [...yes] })
            }
            data-testid="batch-confirm"
          >
            {preparing
              ? "Checking each person with the bank…"
              : pay.busy
                ? "Confirming…"
                : `${confirmWords(b)} with your passkey`}
          </button>
          {passed.rows && (
            <button
              type="button"
              disabled={pay.busy || act.busy}
              onClick={changeList}
              data-testid="batch-change"
            >
              Change the list
            </button>
          )}
          <button
            type="button"
            className="danger"
            disabled={pay.busy || act.busy}
            onClick={() =>
              confirm("Cancel this list? Nobody on it is paid.") &&
              void act.run(
                async () =>
                  setNow(
                    (
                      await post<{ batch: PaymentBatch }>(
                        `/v1/payment-batches/${encodeURIComponent(b.id)}/cancel`,
                      )
                    ).batch,
                  ),
                "List cancelled",
              )
            }
            data-testid="batch-cancel"
          >
            Cancel the list
          </button>
        </div>
      )}
      {prepared && pay.view}
      {asked.length > 0 && prepared && (
        <div className="pay-warn warn" role="alert" data-testid="batch-again">
          {asked.length === 1
            ? "1 person was paid the same amount in the last day"
            : `${asked.length} people were paid the same amount in the last day`}
          : they are not paid yet. Tick "Pay again" on each you mean to pay
          again, then confirm.
        </div>
      )}
      {!prepared && b.status !== "cancelled" && (
        <div className="toolbar">
          {owner && c.waiting > 0 && (
            <button
              type="button"
              className="primary"
              disabled={ok.busy}
              onClick={() => void ok.go({ payments: waitingIds(b) })}
              data-testid="batch-approve"
            >
              {ok.busy ? "Giving the OK…" : "Give the OK for all"}
            </button>
          )}
          {c.failed > 0 && (
            <button
              type="button"
              disabled={make.busy}
              onClick={() => void againList(b.id)}
              data-testid="batch-again-failed"
            >
              Pay the failed ones again
            </button>
          )}
          <button
            type="button"
            disabled={make.busy}
            onClick={() => void make.make({ copy_of: b.id })}
            data-testid="batch-copy"
          >
            Same as last time
          </button>
        </div>
      )}
      {b.status === "cancelled" && (
        <div className="toolbar">
          <button
            type="button"
            disabled={make.busy}
            onClick={() => void make.make({ copy_of: b.id })}
            data-testid="batch-copy"
          >
            Same as last time
          </button>
        </div>
      )}
      {!prepared && ok.view}
      {results?.some((x) => x.code === "changed") && (
        <p className="hint" data-testid="batch-one-by-one">
          A payment to a changed account is approved on its own:{" "}
          <Link to="/approvals">open Approvals</Link>.
        </p>
      )}
      <ErrorBox error={act.error ?? make.error ?? againAct.error} />
      {!prepared && c.waiting > 0 && !owner && (
        <p className="hint" data-testid="batch-waiting">
          {c.waiting === 1 ? "1 payment waits" : `${c.waiting} payments wait`}{" "}
          for the owner's OK in Approvals.
        </p>
      )}

      {held.length > 0 && (
        <section
          className="card"
          aria-labelledby="batch-held-h"
          data-testid="batch-held"
        >
          <h2 id="batch-held-h">Held out: {peopleWords(held.length)}</h2>
          <p className="hint">
            These are not paid with the list. Change the list to fix them, or
            remove them.
          </p>
          <Items label="Held out">
            {held.map((h) => (
              <Row
                key={h.row}
                icon="money"
                tone="warn"
                title={h.payee}
                testid="batch-held-row"
                sub={<span className="sub batch-held-why">{h.problem}</span>}
              >
                <button
                  type="button"
                  className="small"
                  onClick={() => setDismissed((cur) => new Set(cur).add(h.row))}
                >
                  Remove<span className="sr-only"> {h.payee}</span>
                </button>
              </Row>
            ))}
          </Items>
        </section>
      )}

      <section className="card" aria-labelledby="batch-rows-h">
        <h2 id="batch-rows-h">
          {prepared ? "Each person, as the bank named them" : "Each person"}
        </h2>
        {shown.length === 0 ? (
          <EmptyState icon="money">
            Nobody on this list could be paid. Change the list to fix them.
          </EmptyState>
        ) : (
          <Items label="Each person">
            {shown.map((x) => (
              <BatchRow
                key={x.id}
                x={x}
                prepared={prepared}
                problem={problems.get(x.id)?.problem}
                again={
                  prepared && asked.includes(x.id)
                    ? { yes: yes.has(x.id), set: tick(x.id) }
                    : undefined
                }
              />
            ))}
          </Items>
        )}
      </section>
    </>
  );

  async function againList(from: string) {
    await againAct.run(async () => {
      const r = await post<{ batch: PaymentBatch }>(
        `/v1/payment-batches/${encodeURIComponent(from)}/again`,
      );
      navigate(`/payments/batches/${encodeURIComponent(r.batch.id)}`, {
        state: { held: r.batch.held ?? [] } satisfies ListState,
      });
    }, false);
  }
}

function BatchCrumbs() {
  return (
    <p className="crumbs">
      <Link to="/payments" aria-label="Back to Pay people">
        Pay people
      </Link>
    </p>
  );
}

/** One person of a batch: prepared, with what to know before it goes;
 * sent, opening the payment's page. */
function BatchRow({
  x,
  prepared,
  problem,
  again,
}: {
  x: Payment;
  prepared: boolean;
  problem?: string;
  again?: { yes: boolean; set: (v: boolean) => void };
}) {
  const st = rowStatus(x);
  const pill = <span className={`badge sf-status ${st.tone}`}>{st.label}</span>;
  if (prepared) {
    const warns = payWarnings(x).filter((w) => w.key !== "unchecked");
    return (
      <Row
        icon="money"
        tone={
          warns.some((w) => w.tone === "danger")
            ? "danger"
            : problem
              ? "warn"
              : undefined
        }
        title={payeeName(x)}
        side={<span className="item-figure">{naira(x.amount_kobo)}</span>}
        testid="batch-row"
        attrs={{ "data-status": x.status }}
        sub={
          <span className="sub">
            <span className="block">{paymentSub(x)}</span>
            {warns.map((w) => (
              <span
                key={w.key}
                className={`block pay-warn ${w.tone} batch-problem`}
                data-testid={`batch-warn-${w.key}`}
              >
                {w.words}
              </span>
            ))}
            {problem && (
              <span
                className="block pay-warn warn batch-problem"
                data-testid="batch-row-problem"
              >
                {problem}
              </span>
            )}
          </span>
        }
      >
        {again && (
          <label className="switch-line">
            <input
              type="checkbox"
              checked={again.yes}
              onChange={(e) => again.set(e.target.checked)}
              data-testid="batch-pay-again"
            />{" "}
            <span>
              Pay again<span className="sr-only"> to {payeeName(x)}</span>
            </span>
          </label>
        )}
      </Row>
    );
  }
  return (
    <Item
      to={`/payments/${encodeURIComponent(x.id)}`}
      icon="money"
      tone={st.tone}
      title={payeeName(x)}
      side={naira(x.amount_kobo)}
      sub={
        problem
          ? `${paymentSub(x)} · ${problem}`
          : x.reason
            ? `${paymentSub(x)} · ${x.reason}`
            : paymentSub(x)
      }
      sideSub={pill}
      testid="batch-row"
    />
  );
}

/** On the Pay people page: the lists paid before, newest first, each
 * with Same as last time. */
export function BatchesCard() {
  const list = useLoad(
    () =>
      get<{ batches: PaymentBatch[] }>("/v1/payment-batches").catch(() => ({
        batches: [] as PaymentBatch[],
      })),
    [],
  );
  const make = useMakeBatch();
  const batches = list.data?.batches.filter((b) => b.count > 0) ?? [];
  if (batches.length === 0) return null;
  return (
    <section className="card" aria-labelledby="batches-h" data-testid="batches">
      <h2 id="batches-h">Lists paid before</h2>
      <Items label="Lists paid before">
        {batches.map((b) => {
          const tag = listTag(b);
          const name = batchName(b);
          return (
            <Row
              key={b.id}
              icon="members"
              tone={tag.tone === "ok" ? undefined : tag.tone}
              title={name}
              side={<span className="item-figure">{naira(b.total_kobo)}</span>}
              testid="batch"
              sub={
                <span className="sub">
                  {peopleWords(b.count)} ·{" "}
                  {fmtTime(b.confirmed_at ?? b.created_at)}
                  <span className="block">
                    <span className={`badge sf-status ${tag.tone}`}>
                      {tag.label}
                    </span>{" "}
                    {listWords(b)}
                  </span>
                </span>
              }
            >
              <Link
                className="button small"
                to={`/payments/batches/${encodeURIComponent(b.id)}`}
              >
                Open<span className="sr-only"> {name}</span>
              </Link>
              <button
                type="button"
                className="small"
                disabled={make.busy}
                onClick={() => void make.make({ copy_of: b.id })}
                data-testid="batch-copy"
              >
                Same as last time<span className="sr-only"> for {name}</span>
              </button>
            </Row>
          );
        })}
      </Items>
      <ErrorBox error={make.error} />
    </section>
  );
}
