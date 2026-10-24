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
import { Copy } from "./Forms";

// Students' account numbers (docs/spec/schools.md 6, B4;
// docs/schools.md#account-numbers): the owner's issuing connection in
// Schools settings, and each student's own account number on their page.
// The batch page is SchoolAccounts.tsx.

export type SchoolSettingsWithIssuing = SchoolSettings & {
  issuing?: SchoolIssuing;
  issuers?: SchoolIssuer[];
};

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
        <EmptyState icon="connections" action={<Link to="/connections">Connections</Link>}>
          No connection here can issue account numbers yet. Connect iswallet, Paystack, Flutterwave or Moniepoint in prod first.
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
                  {i.rail} ({i.connection})
                </option>
              ))}
            </select>
          </Field>
          {chosen?.needs_account && (
            <Field label="Wallet the numbers pay into" hint={`The wallet's id on ${chosen.rail}.`}>
              <input value={value.account ?? ""} onChange={(e) => setForm({ ...value, account: e.target.value })} placeholder="wal_…" />
            </Field>
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
                <strong>
                  {i.rail} ({i.connection})
                </strong>
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
  const act = useAction();
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
