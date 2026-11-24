// Payroll settings (P4b-1, docs/spec/chat-pay-people.md): connecting
// Payrolla in one step (signing up there in the same step for a business
// not on Payrolla yet), Reconnect Payrolla for a business connected with a
// key or whose connection ended, and who may approve the payroll in
// Payrolla, with Link my Payrolla account by one code. Payrolla's code is
// sent to Payrolla once and kept nowhere.

import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ApiError, del, get, post } from "../api";
import { PayrollSetupSection } from "./PayrollSchedules";
import { ErrorBox, Field, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import {
  NEW_CODE_NEEDED,
  approverSub,
  canApproveHere,
  codeDigits,
  connectBody,
  connectionState,
  linkedWords,
  newKey,
  sortApprovers,
  type LinkCodeSent,
  type PayrollApprover,
  type PayrollApprovers,
  type PayrollConnection,
} from "../lib/payroll";

const FROM_KEY = "taskiem.payrolla.from-key";

function remember(fromKey: boolean) {
  try {
    if (fromKey) sessionStorage.setItem(FROM_KEY, "1");
    else sessionStorage.removeItem(FROM_KEY);
  } catch {
    /* private mode: the reminder is not shown */
  }
}

function recalled(): boolean {
  try {
    const v = sessionStorage.getItem(FROM_KEY) === "1";
    sessionStorage.removeItem(FROM_KEY);
    return v;
  } catch {
    return false;
  }
}

/** Connect Payrolla, or Reconnect Payrolla: off to Payrolla, and back. */
function ConnectCard({ conn, reconnect }: { conn: PayrollConnection; reconnect: boolean }) {
  const modes = conn.modes;
  const [mode, setMode] = useState(conn.connection?.test ? "test" : (modes[0] ?? ""));
  const [signUp, setSignUp] = useState(false);
  const [rc, setRc] = useState("");
  const go = useAction();
  const fromKey = connectionState(conn) === "key";
  const start = () =>
    void go.run(async () => {
      const r = await post<{ url: string }>("/v1/payroll/connect", connectBody(reconnect ? "" : mode, signUp, rc));
      remember(fromKey);
      window.location.assign(r.url);
    }, false);
  if (modes.length === 0)
    return (
      <p className="hint" data-testid="payroll-connect-unavailable">
        Connecting in one step is not set up on this Taskiem yet. <Link to="/connections?connect=payrolla">Use a key instead</Link>.
      </p>
    );
  return (
    <div data-testid="payroll-connect-card">
      {!reconnect && modes.length > 1 && (
        <fieldset className="field">
          <legend>Which Payrolla</legend>
          {modes.map((m) => (
            <label key={m} className="batch-pick">
              <input type="radio" name="payrolla-mode" value={m} checked={mode === m} onChange={() => setMode(m)} />{" "}
              {m === "live" ? "Live (real money)" : "Test (practice)"}
            </label>
          ))}
        </fieldset>
      )}
      {!reconnect && signUp && (
        <>
          <p className="hint">Payrolla opens with your business's name and your email filled in. Add your RC number if you have one.</p>
          <Field label="RC number (if you have one)">
            <input value={rc} maxLength={20} onChange={(e) => setRc(e.target.value)} data-testid="payroll-rc" />
          </Field>
        </>
      )}
      <ErrorBox error={go.error} />
      <div className="toolbar">
        <button type="button" className="primary" disabled={go.busy} onClick={start} data-testid={reconnect ? "payroll-reconnect" : "payroll-connect-now"}>
          {reconnect ? "Reconnect Payrolla" : signUp ? "Sign up and connect" : "Connect Payrolla"}
        </button>
        {!reconnect && (
          <button type="button" className="quiet" onClick={() => setSignUp(!signUp)} data-testid="payroll-sign-up">
            {signUp ? "I'm already on Payrolla" : "I'm not on Payrolla yet"}
          </button>
        )}
      </div>
      {!reconnect && (
        <p className="hint">
          Already have a key from Payrolla? <Link to="/connections?connect=payrolla">Use a key instead</Link>.
        </p>
      )}
    </div>
  );
}

/** How Payroll is connected, and what the owner can do about it. */
function ConnectionSection({ conn, onChanged }: { conn: PayrollConnection; onChanged: () => void }) {
  const state = connectionState(conn);
  const owner = conn.is_owner;
  const [asking, setAsking] = useState(false);
  const off = useAction();
  const company = conn.connection?.company?.name;
  return (
    <section className="card" aria-labelledby="payroll-conn-h" data-testid="payroll-connection" data-state={state}>
      <h2 id="payroll-conn-h">Payrolla</h2>
      {state === "none" && (
        <>
          <p>Connect your business's Payrolla to see and run your payroll here. You allow Taskiem in Payrolla, then come back.</p>
          {owner ? <ConnectCard conn={conn} reconnect={false} /> : <p className="hint">Ask the owner to connect Payrolla.</p>}
        </>
      )}
      {state === "ended" && (
        <div className="warning" role="note" data-testid="payroll-ended">
          {owner ? (
            <>
              <p style={{ marginTop: 0 }}>Payrolla is no longer connected. Connect again to see and run your payroll here.</p>
              <ConnectCard conn={conn} reconnect />
            </>
          ) : (
            <p style={{ margin: 0 }}>Payroll isn't connected right now. The owner has been told.</p>
          )}
        </div>
      )}
      {state === "key" && (
        <>
          <p data-testid="payroll-company-name">
            <strong>{company || "Your company"}</strong> · connected with a key
          </p>
          {owner && (
            <div className="notice" data-testid="payroll-key-note">
              <p style={{ marginTop: 0 }}>Connect again in one step so you can run payroll here.</p>
              <ConnectCard conn={conn} reconnect />
            </div>
          )}
        </>
      )}
      {state === "one_step" && (
        <>
          <p data-testid="payroll-company-name">
            <strong>{company || "Your company"}</strong> · connected
            {conn.connection?.test && <span className="badge sf-status info"> Test</span>}
          </p>
          {owner && !asking && (
            <div className="toolbar">
              <button type="button" className="quiet" onClick={() => setAsking(true)} data-testid="payroll-disconnect">
                Disconnect Payrolla
              </button>
            </div>
          )}
          {owner && asking && (
            <div className="warning" role="note">
              <p style={{ marginTop: 0 }}>Taskiem stops seeing your payroll, and Payrolla is told to forget Taskiem. Your payroll stays in Payrolla.</p>
              <ErrorBox error={off.error} />
              <div className="toolbar">
                <button
                  type="button"
                  className="danger"
                  disabled={off.busy}
                  data-testid="payroll-disconnect-yes"
                  onClick={() =>
                    void off.run(async () => {
                      await del(`/v1/connections/${encodeURIComponent(conn.connection?.id ?? "")}?force=true`);
                      setAsking(false);
                      onChanged();
                    }, "Payrolla disconnected")
                  }
                >
                  Disconnect
                </button>
                <button type="button" onClick={() => setAsking(false)}>
                  Keep it
                </button>
              </div>
            </div>
          )}
        </>
      )}
    </section>
  );
}

/** Link my Payrolla account: Payrolla sends a code to that account's own
 * phone, email or authenticator app, and the person types it here. */
function LinkSheet({ who, onClose, onLinked }: { who: PayrollApprover; onClose: () => void; onLinked: () => void }) {
  const [sent, setSent] = useState<LinkCodeSent>();
  const [code, setCode] = useState("");
  const [needNew, setNeedNew] = useState(false);
  const [sheetKey, setSheetKey] = useState(newKey);
  const send = useAction();
  const confirm = useAction();
  const path = `/v1/payroll/approvers/${encodeURIComponent(who.user_id)}/link`;
  const sendCode = (key: string) =>
    void send.run(async () => {
      setNeedNew(false);
      setCode("");
      confirm.clear();
      setSent(await post<LinkCodeSent>(path, { key }));
    }, false);
  const typed = codeDigits(code);
  const submit = () =>
    void confirm.run(async () => {
      try {
        // Each try at a code is its own submission.
        await post(`${path}/confirm`, { challenge_id: sent?.challenge.challenge_id, code: typed, key: newKey() });
      } catch (e) {
        if (e instanceof ApiError && NEW_CODE_NEEDED.has(String(e.body.code ?? ""))) setNeedNew(true);
        throw e;
      }
      onLinked();
    }, false);
  return (
    <Modal title={`Link to ${who.name} in Payrolla`} onClose={onClose}>
      {!sent && (
        <>
          <p>Payrolla sends a code to {who.name}'s own phone, email or authenticator app. Only they can finish this.</p>
          <ErrorBox error={send.error} />
          <div className="toolbar">
            <button type="button" className="primary" disabled={send.busy} onClick={() => sendCode(sheetKey)} data-testid="payroll-link-send">
              {send.busy ? "Sending…" : "Send me a code"}
            </button>
            <button type="button" onClick={onClose}>
              Cancel
            </button>
          </div>
        </>
      )}
      {sent && (
        <>
          <p role="status" data-testid="payroll-link-sent">
            {sent.sent_to} Type it here.
          </p>
          {sent.safer && <p className="hint">A code from an authenticator app is safer: set one up in Payrolla.</p>}
          <Field label="The code from Payrolla">
            <input
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={7}
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/[^\d ]/g, ""))}
              data-testid="payroll-link-code"
            />
          </Field>
          <ErrorBox error={confirm.error} />
          <div className="toolbar">
            {!needNew && (
              <button type="button" className="primary" disabled={confirm.busy || typed.length !== 6} onClick={submit} data-testid="payroll-link-confirm">
                Link my account
              </button>
            )}
            <button
              type="button"
              className={needNew ? "primary" : "quiet"}
              disabled={send.busy}
              onClick={() => {
                const k = newKey();
                setSheetKey(k);
                sendCode(k);
              }}
              data-testid="payroll-link-resend"
            >
              Send a new code
            </button>
          </div>
          <ErrorBox error={send.error} />
        </>
      )}
    </Modal>
  );
}

/** Who may approve and pay in Payrolla, and who they are in Taskiem. */
function ApproversSection() {
  const list = useLoad(() => get<PayrollApprovers>("/v1/payroll/approvers"), []);
  const [linking, setLinking] = useState<PayrollApprover>();
  const unlink = useAction();
  const d = list.data;
  const rows = sortApprovers(d?.approvers ?? []);
  return (
    <section className="card" aria-labelledby="payroll-approvers-h" data-testid="payroll-approvers">
      <h2 id="payroll-approvers-h">Who can approve in Payrolla</h2>
      <p className="hint">Approving and paying from Taskiem is done as your own Payrolla account. Link it once, with a code Payrolla sends you.</p>
      <ErrorBox error={list.error} />
      {!d && !list.error && <Skeleton rows={3} label="Loading who can approve…" />}
      {d && rows.length === 0 && <p className="hint">No one in Payrolla can approve payroll yet.</p>}
      {d && rows.length > 0 && (
        <ul className="items" aria-label="Who can approve in Payrolla">
          {rows.map((a) => {
            const mayLink = d.may_link && !d.me && !a.linked && a.can_confirm;
            const mayUnlink = !!a.linked && (a.me || d.is_owner);
            return (
              <Row
                key={a.user_id}
                letter={a.name.charAt(0)}
                tone={a.me ? "ok" : undefined}
                title={a.name}
                sub={<div className="sub">{`${approverSub(a)} · ${linkedWords(a)}`}</div>}
                testid="payroll-approver"
                attrs={{ "data-linked": a.linked ? "yes" : "no" }}
              >
                {(mayLink || mayUnlink) && (
                  <div className="toolbar">
                    {mayLink && (
                      <button type="button" className={a.suggested ? "small primary" : "small"} onClick={() => setLinking(a)} data-testid="payroll-link-open">
                        Link my Payrolla account<span className="sr-only">: {a.name}</span>
                      </button>
                    )}
                    {mayUnlink && (
                      <button
                        type="button"
                        className="small quiet"
                        disabled={unlink.busy}
                        data-testid="payroll-unlink"
                        onClick={() =>
                          void unlink.run(async () => {
                            await del(`/v1/payroll/approvers/${encodeURIComponent(a.user_id)}/link?key=${encodeURIComponent(newKey())}`);
                            list.reload();
                          }, "Unlinked")
                        }
                      >
                        Unlink<span className="sr-only"> {a.name}</span>
                      </button>
                    )}
                  </div>
                )}
              </Row>
            );
          })}
        </ul>
      )}
      <ErrorBox error={unlink.error} />
      {linking && (
        <LinkSheet
          who={linking}
          onClose={() => setLinking(undefined)}
          onLinked={() => {
            setLinking(undefined);
            list.reload();
          }}
        />
      )}
    </section>
  );
}

/** /payroll/settings: Payrolla's connection, and who can approve. */
export function PayrollSettingsPage() {
  const [params] = useSearchParams();
  const conn = useLoad(() => get<PayrollConnection>("/v1/payroll/connection"), []);
  const [fromKey] = useState(() => (params.get("linked") ? recalled() : false));
  const c = conn.data;
  const linkedNow = !!params.get("linked");
  const refused = params.get("error");
  return (
    <>
      <p className="crumbs">
        <Link to="/payroll" aria-label="Back to Payroll">
          Payroll
        </Link>
      </p>
      <PageHeader title="Payroll settings" description="How Taskiem reaches your Payrolla, how your payroll is set up there, and who approves it." />
      {linkedNow && (
        <div className="notice" role="status" data-testid="payroll-connected-now">
          Payrolla is connected.
          {fromKey && <> Now remove Taskiem's old key in Payrolla → Settings → Connected apps.</>}
        </div>
      )}
      {refused && (
        <div className="error" role="alert" data-testid="payroll-connect-refused">
          {refused}
        </div>
      )}
      <ErrorBox error={conn.error} />
      {!c && !conn.error && <Skeleton rows={3} label="Loading…" />}
      {c && <ConnectionSection conn={c} onChanged={conn.reload} />}
      {c?.connected && <PayrollSetupSection />}
      {c && canApproveHere(c) && <ApproversSection />}
    </>
  );
}
