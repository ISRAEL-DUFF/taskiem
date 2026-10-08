import { useCallback, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { ApiError } from "./api";
import { passkeysSupported, stepUpAssert } from "./passkeys";
import { ErrorBox, Field, Modal, useAction } from "./ui";

/** What a step-up is given: a passkey assertion or an authenticator code. */
export type StepUpProof = { passkey?: Awaited<ReturnType<typeof stepUpAssert>>; totp?: string };

interface Pending {
  title: string;
  operation: string;
  target: string;
  methods: string[];
  message: string;
  send: (p: StepUpProof) => Promise<unknown>;
  resolve: (v: unknown) => void;
  reject: (e: unknown) => void;
}

/** Changes the server holds to step-up (encryption keys): the request is
 * sent as it is, and when the answer asks for step-up, the person confirms
 * with a passkey (asked for this operation on this target only) or an
 * authenticator code, and the request is sent again with it. */
export function useStepUp(): { request: <T>(title: string, send: (p: StepUpProof) => Promise<T>) => Promise<T>; modal: ReactNode } {
  const [pending, setPending] = useState<Pending | null>(null);
  const ref = useRef<Pending | null>(null);
  const request = useCallback(<T,>(title: string, send: (p: StepUpProof) => Promise<T>): Promise<T> => {
    return send({}).catch((e: unknown) => {
      if (!(e instanceof ApiError) || e.status !== 403 || e.body.step_up !== "required" || typeof e.body.operation !== "string") throw e;
      return new Promise<T>((resolve, reject) => {
        const p: Pending = {
          title,
          operation: e.body.operation as string,
          target: String(e.body.target ?? ""),
          methods: Array.isArray(e.body.methods) ? (e.body.methods as string[]) : [],
          message: e.message,
          send,
          resolve: resolve as (v: unknown) => void,
          reject,
        };
        ref.current = p;
        setPending(p);
      });
    });
  }, []);
  const close = () => {
    ref.current?.reject(new Error("Not confirmed: nothing was changed."));
    ref.current = null;
    setPending(null);
  };
  const done = (v: unknown) => {
    ref.current?.resolve(v);
    ref.current = null;
    setPending(null);
  };
  return { request, modal: pending ? <Confirm p={pending} onClose={close} onDone={done} /> : null };
}

function Confirm({ p, onClose, onDone }: { p: Pending; onClose: () => void; onDone: (v: unknown) => void }) {
  const [code, setCode] = useState("");
  const act = useAction();
  const send = (proof: StepUpProof) => act.run(async () => onDone(await p.send(proof)));
  const none = p.methods.length === 0;
  return (
    <Modal title={`Confirm: ${p.title}`} onClose={onClose}>
      {none ? (
        <p>
          {p.message} <Link to="/account">Go to Account</Link>.
        </p>
      ) : (
        <p>Confirm it is you. The confirmation is good for this change only.</p>
      )}
      {p.methods.includes("passkey") && passkeysSupported() && (
        <p>
          <button className="primary" disabled={act.busy} onClick={() => void act.run(async () => onDone(await p.send({ passkey: await stepUpAssert(p.operation, p.target) })))}>
            Confirm with your passkey
          </button>
        </p>
      )}
      {p.methods.includes("totp") && (
        <form
          className="row"
          style={{ alignItems: "flex-end" }}
          onSubmit={(e) => {
            e.preventDefault();
            void send({ totp: code });
          }}
        >
          <Field label="Code from your authenticator app">
            <input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
          </Field>
          <button className="primary" type="submit" disabled={act.busy || code.length !== 6}>
            Confirm
          </button>
        </form>
      )}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}
