import { useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, post, put } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, Skeleton, useAction, useLoad } from "../ui";
import { plainMessage, schoolAccess, type SchoolSettings } from "../lib/schools";
import {
  accountStatus,
  feeLine,
  idProblem,
  issuingProblem,
  issuingRequest,
  nameNote,
  railOf,
  spacedNumber,
  type SchoolIssuer,
  type SchoolIssuing,
  type StudentAccount,
} from "../lib/schoolAccounts";
import { sentLine, type FamilySent } from "../lib/schoolFamily";
import { Copy } from "./Forms";

// Students' account numbers (docs/spec/schools.md 6, B4;
// docs/schools.md#account-numbers): the owner's issuing connection in
// Schools settings, and each student's own account number on their page.
// The batch page is SchoolAccounts.tsx.

export type SchoolSettingsWithIssuing = SchoolSettings & {
  issuing?: SchoolIssuing;
  issuers?: SchoolIssuer[];
};

/** The connection Taskiem holds for a business that receives money with it (decision 0052). */
const TASKIEM_CHANNEL = "iswallet/taskiem";

/** An issuer as the setting names it: Taskiem's own wallet by that name. */
const issuerName = (i: SchoolIssuer) => (i.connection === TASKIEM_CHANNEL ? "Taskiem (your wallet with Taskiem)" : `${i.rail} (${i.connection})`);

/** Runs a request and words the server's refusal as a sentence. */
export async function plainly<T>(p: Promise<T>): Promise<T> {
  try {
    return await p;
  } catch (e) {
    if (e instanceof ApiError && (e.status === 400 || e.status === 409 || e.status === 422)) e.message = plainMessage(e.message);
    throw e;
  }
}

/** Schools settings: the connection that issues students' account numbers, and siblings sharing family credit (owners). */
export function IssuingSettings() {
  const settings = useLoad(() => get<SchoolSettingsWithIssuing>("/v1/settings/schools"), []);
  const [form, setForm] = useState<SchoolIssuing>();
  const act = useAction();
  const issuers = settings.data?.issuers ?? [];
  const value: SchoolIssuing = form ?? settings.data?.issuing ?? { connection: "" };
  const chosen = issuers.find((i) => i.connection === value.connection);
  const problem = issuingProblem(value, issuers);
  return (
    <section className="card" aria-labelledby="issuing-heading">
      <h2 id="issuing-heading" style={{ marginTop: 0 }}>
        Students' account numbers
      </h2>
      <p className="hint">
        Each student gets their own bank account number, so a parent's transfer lands on the right child by itself. Choose the connection that issues them; its rail's own checks
        (your business's BVN, director and registration) are done with the rail when you connect it.
      </p>
      <ErrorBox error={settings.error} />
      {!settings.data && !settings.error && <Skeleton rows={2} label="Loading the settings…" />}
      {settings.data && issuers.length === 0 && (
        <EmptyState icon="connections" action={<Link to="/settings/payments">Receive money with Taskiem</Link>}>
          No connection here can issue account numbers yet. Receive money with Taskiem (no account to sign up for), or connect iswallet, Paystack, Flutterwave or Moniepoint in
          prod in <Link to="/connections">Connections</Link>.
        </EmptyState>
      )}
      {settings.data && issuers.length > 0 && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (problem) return;
            void act.run(async () => {
              await plainly(
                put("/v1/settings/schools", {
                  enabled: settings.data?.enabled ?? true,
                  issuing: issuingRequest(value, issuers),
                }),
              );
              setForm(undefined);
              settings.reload();
            }, "Account number settings saved");
          }}
        >
          <Field label="Issuing connection">
            <select value={value.connection} onChange={(e) => setForm({ ...value, connection: e.target.value })}>
              <option value="">None chosen</option>
              {issuers.map((i) => (
                <option key={i.connection} value={i.connection}>
                  {issuerName(i)}
                </option>
              ))}
            </select>
          </Field>
          {chosen?.needs_account && chosen.connection !== TASKIEM_CHANNEL && (
            <Field label="Wallet the numbers pay into" hint={`The wallet's id on ${chosen.rail}.`}>
              <input value={value.account ?? ""} onChange={(e) => setForm({ ...value, account: e.target.value })} placeholder="wal_…" />
            </Field>
          )}
          {chosen?.creates_wallet && chosen.connection !== TASKIEM_CHANNEL && !settings.data?.issuing?.account && (
            <CreateWallet
              connection={chosen.connection}
              rail={chosen.rail}
              onDone={() => {
                setForm(undefined);
                settings.reload();
              }}
            />
          )}
          <label className="row">
            <input type="checkbox" checked={!!value.siblings_share_credit} onChange={(e) => setForm({ ...value, siblings_share_credit: e.target.checked })} /> When a student's
            account takes more than their own fees, count the rest towards their brothers' and sisters' fees, oldest first
          </label>
          <p className="hint">Off, what is left stays the child's own credit, counted towards their next invoice.</p>
          {problem && (
            <div className="error" role="alert">
              {problem}
            </div>
          )}
          <button type="submit" className="primary" disabled={act.busy || !!problem}>
            Save
          </button>
        </form>
      )}
      {issuers.length > 0 && (
        <>
          <h3>What each can do</h3>
          <ul className="plain-list" data-testid="issuers">
            {issuers.map((i) => (
              <li key={i.connection}>
                <strong>{issuerName(i)}</strong>
                : {i.requirement} {feeLine(i)}{" "}
                {i.look_back && (i.webhook ? "Payments arrive by its webhook, and are also read back every 15 minutes." : "Payments are read back every 15 minutes.")}
              </li>
            ))}
          </ul>
        </>
      )}
      <ErrorBox error={act.error} />
    </section>
  );
}

/** Creates the school's wallet on the issuing connection instead of
 * pasting its id (POST /v1/settings/schools/wallet): the director's BVN and
 * the business email go to the rail once, and Taskiem keeps neither. */
function CreateWallet({ connection, rail, onDone }: { connection: string; rail: string; onDone: () => void }) {
  const [open, setOpen] = useState(false);
  const [ownerType, setOwnerType] = useState("client_end_user_business");
  const [email, setEmail] = useState("");
  const [bvn, setBvn] = useState("");
  const act = useAction();
  if (!open)
    return (
      <p>
        <button type="button" onClick={() => setOpen(true)}>
          Create the school's wallet
        </button>{" "}
        <span className="hint">No wallet yet? {rail} can make one for the school now.</span>
      </p>
    );
  return (
    <fieldset className="card" data-testid="create-wallet">
      <legend>The school's wallet on {rail}</legend>
      <label className="row">
        <input type="radio" name="owner-type" checked={ownerType === "client_end_user_business"} onChange={() => setOwnerType("client_end_user_business")} /> It holds
        parents' fees: kept in the school's own account at {rail}'s bank. Payments show in the balance at once; the money itself reaches that account within minutes.
      </label>
      <label className="row">
        <input type="radio" name="owner-type" checked={ownerType === "client_end_business"} onChange={() => setOwnerType("client_end_business")} /> It holds only the
        school's own money: kept with {rail}'s, and counted as the school's.
      </label>
      <p className="hint">This cannot easily be changed later: ask {rail} first if you are unsure.</p>
      <Field label="The school's business email">
        <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" />
      </Field>
      <Field label="The director's BVN" hint={`11 digits. Sent to ${rail} once to open the wallet; Taskiem does not keep it.`}>
        <input value={bvn} onChange={(e) => setBvn(e.target.value.replace(/\D/g, "").slice(0, 11))} inputMode="numeric" autoComplete="off" />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={act.busy || bvn.length !== 11 || !email.includes("@")}
          onClick={() =>
            void act.run(async () => {
              await plainly(post("/v1/settings/schools/wallet", { connection, owner_type: ownerType, business_email: email, bvn }));
              setBvn("");
              onDone();
            }, "The school's wallet is ready: students' numbers will pay into it")
          }
        >
          {act.busy ? "Creating…" : "Create the wallet"}
        </button>
        <button type="button" onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
    </fieldset>
  );
}

/** A student's own account number, on their page (GET /v1/schools/students/{id}/accounts). */
export function StudentAccountPanel({ studentID }: { studentID: string }) {
  const { can } = useAuth();
  const may = schoolAccess(can);
  const [reveal, setReveal] = useState(false);
  const accounts = useLoad(
    () =>
      get<{
        accounts: StudentAccount[];
        issuing_connection: string;
        revealed: boolean;
      }>(`/v1/schools/students/${encodeURIComponent(studentID)}/accounts${reveal ? "?reveal=true" : ""}`),
    [studentID, reveal],
  );
  const [bvn, setBvn] = useState("");
  const [nin, setNin] = useState("");
  const [sent, setSent] = useState<FamilySent>();
  const act = useAction();
  const send = useAction();
  const data = accounts.data;
  const issuing = data?.issuing_connection ?? "";
  const mine = data?.accounts.find((a) => a.connection === issuing) ?? data?.accounts[0];
  const issued = data?.accounts.filter((a) => a.status === "issued") ?? [];
  // What the issuing connection's rail needs: a parent's BVN or NIN on Flutterwave.
  const settings = useLoad(() => (may.manage ? get<SchoolSettingsWithIssuing>("/v1/settings/schools") : Promise.resolve(undefined)), [may.manage]);
  const needsID = !!settings.data?.issuers?.find((i) => i.connection === issuing)?.needs_bvn;
  const idErr = needsID ? idProblem(bvn, nin) : null;
  const issue = (retry: boolean) =>
    act.run(async () => {
      const body: Record<string, unknown> = {};
      if (needsID) {
        if (bvn.trim()) body.bvn = bvn.trim();
        if (nin.trim()) body.nin = nin.trim();
      }
      if (retry) body.retry = true;
      try {
        await plainly(post<StudentAccount>(`/v1/schools/students/${encodeURIComponent(studentID)}/account`, body));
      } finally {
        setBvn("");
        setNin("");
        accounts.reload();
      }
    }, "Account number issued");
  return (
    <section className="card" aria-labelledby="student-account">
      <h2 id="student-account">Account number</h2>
      <ErrorBox error={accounts.error} />
      {!data && !accounts.error && <Skeleton rows={2} label="Loading the account number…" />}
      {data && issued.length === 0 && (
        <p className="hint">{issuing ? "No account number yet." : "No account number yet. An owner chooses the connection that issues them in Schools > Settings."}</p>
      )}
      {issued.map((a) => (
        <div key={a.connection} data-testid="student-account-number">
          <dl className="kv">
            <dt>Account number</dt>
            <dd>
              <span className="mono">{spacedNumber(a.account_number ?? "")}</span>{" "}
              <Copy text={a.account_number ?? ""} label={`Copy the account number ${a.account_number ?? ""}`} />
            </dd>
            <dt>Bank</dt>
            <dd>{a.bank_name || "Not given by the rail"}</dd>
            <dt>Name</dt>
            <dd>{a.account_name}</dd>
            <dt>On</dt>
            <dd>{a.connection}</dd>
          </dl>
          {nameNote(a, railOf(a.connection)) && <p className="hint">{nameNote(a, railOf(a.connection))}</p>}
        </div>
      ))}
      {data && issued.length > 0 && !data.revealed && may.reveal && (
        <button type="button" onClick={() => setReveal(true)}>
          Show the account's name
        </button>
      )}
      {data && issued.length > 0 && may.manage && (
        <div style={{ marginTop: 12 }}>
          <button
            type="button"
            className="secondary"
            disabled={send.busy}
            onClick={() =>
              void send.run(async () => {
                setSent(await plainly(post<FamilySent>(`/v1/schools/students/${encodeURIComponent(studentID)}/account/send`, {})));
              }, false)
            }
          >
            Send the account number to the family
          </button>
          <div role="status">
            {sent && (
              <div className="notice" data-testid="student-account-sent">
                {sentLine(sent).replace(" Copy the link below to pass it on.", "")}
              </div>
            )}
          </div>
          <ErrorBox error={send.error} />
        </div>
      )}
      {mine && mine.status !== "issued" && (
        <div className="notice" data-testid="student-account-problem">
          <span className={`badge sf-status ${accountStatus(mine.status).tone}`}>{accountStatus(mine.status).label}</span> {mine.error ?? ""}
        </div>
      )}
      {may.manage && issuing && (!mine || mine.status !== "issued") && (
        <form
          className="toolbar"
          style={{ marginTop: 12 }}
          onSubmit={(e) => {
            e.preventDefault();
            if (idErr) return;
            void issue(mine?.status === "unknown");
          }}
        >
          {needsID && (
            <>
              <Field label="Parent's BVN" hint="Sent to Flutterwave for this account, never kept.">
                <input inputMode="numeric" autoComplete="off" value={bvn} onChange={(e) => setBvn(e.target.value)} maxLength={11} />
              </Field>
              <Field label="or NIN">
                <input inputMode="numeric" autoComplete="off" value={nin} onChange={(e) => setNin(e.target.value)} maxLength={11} />
              </Field>
            </>
          )}
          <button type="submit" className="primary" disabled={act.busy || !!idErr}>
            {mine?.status === "unknown" ? "I checked: issue it again" : mine?.status === "failed" ? "Try again" : "Issue the account number"}
          </button>
        </form>
      )}
      <ErrorBox error={act.error} />
    </section>
  );
}
