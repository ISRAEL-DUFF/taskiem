import { useState } from "react";
import { post } from "../api";
import { useAuth } from "../auth";
import { sentWords, showPhone, toE164 } from "../lib/phone";
import { toast } from "../toast";
import { ErrorBox, Field, Modal, useAction } from "../ui";
import { ProofStep } from "./Switch";
import { CodeField, plainly, useCountdown } from "./WayIn";

// Settings > Phone > Change number (docs/spec/taskiem-for-everyone.md §5.5):
// a code to the new number, and where this device has not proved itself, a
// code to the old one too, or another proof (a passkey, an email link, the
// chat PIN). The account keeps everything.

interface Started {
  sent_via: string;
  old_sent_via?: string;
  needs_old_code: boolean;
}

/** The phone's row in Settings: the number, and Change number (or Add). */
export function PhoneSetting() {
  const { me } = useAuth();
  const [open, setOpen] = useState(false);
  const phone = me?.user?.phone ?? null;
  return (
    <>
      <div className="setting-line" data-testid="my-phone">
        <div className="grow">
          <strong>Phone</strong>
          <div className="sub">{phone ? showPhone(phone) : "No phone yet: add one to sign in with a code."}</div>
        </div>
        <button type="button" onClick={() => setOpen(true)}>
          {phone ? "Change number" : "Add a phone"}
        </button>
      </div>
      {open && <ChangeNumber onClose={() => setOpen(false)} />}
    </>
  );
}

export function ChangeNumber({ onClose }: { onClose: () => void }) {
  const { me, refresh } = useAuth();
  const old = me?.user?.phone ?? null;
  const [step, setStep] = useState<"new" | "old" | "proof" | "code">("new");
  const [typed, setTyped] = useState("");
  const [phone, setPhone] = useState("");
  const [sent, setSent] = useState<Started | null>(null);
  const [needsOld, setNeedsOld] = useState(false);
  const [code, setCode] = useState("");
  const [oldCode, setOldCode] = useState("");
  const [left, wait] = useCountdown();
  const act = useAction();
  const start = (number: string, confirmOld: boolean, via?: "whatsapp" | "sms") =>
    plainly(async () => {
      const r = await post<Started>("/v1/me/phone/start", {
        phone: number,
        ...(confirmOld ? { confirm_old: true } : {}),
        ...(via === "sms" ? { via } : {}),
      });
      setSent(r);
      setCode("");
      setOldCode("");
      wait(60);
      return r;
    });

  if (step === "proof")
    return (
      <ProofStep
        ways={me?.second_proof?.ways ?? { passkey: false, email: false, pin: false }}
        onClose={() => setStep("old")}
        onProved={async () => {
          setNeedsOld(false);
          setStep("code");
          await refresh();
        }}
      />
    );

  return (
    <Modal title={old ? "Change your number" : "Add your phone"} onClose={onClose}>
      <div data-testid="change-number">
        {step === "new" && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(async () => {
                const n = toE164(typed);
                if (!n) throw new Error("Enter the new number, with its country code if it is not Nigerian.");
                const r = await start(n, false);
                setPhone(n);
                setNeedsOld(r.needs_old_code);
                setStep(r.needs_old_code ? "old" : "code");
              }, false);
            }}
          >
            <p>Your account keeps everything: your chats, orders and businesses.</p>
            <Field label="New phone number" hint="A Nigerian number can start with 0. For another country, start with + and its code.">
              <input type="tel" inputMode="tel" autoComplete="tel" value={typed} onChange={(e) => setTyped(e.target.value)} required autoFocus />
            </Field>
            <ErrorBox error={act.error} />
            <button className="primary" type="submit" disabled={act.busy}>
              {act.busy ? "Sending…" : "Send a code"}
            </button>
          </form>
        )}
        {step === "old" && (
          <>
            <p>
              To keep your account safe, we also check it's you. Do you still have your old number{old ? `, ${showPhone(old)}` : ""}?
            </p>
            <ErrorBox error={act.error} />
            <p>
              <button
                type="button"
                className="primary"
                disabled={act.busy}
                onClick={() =>
                  void act.run(async () => {
                    await start(phone, true);
                    setStep("code");
                  }, false)
                }
              >
                Yes, send it a code
              </button>
            </p>
            <p>
              <button type="button" disabled={act.busy} onClick={() => setStep("proof")}>
                No, prove it's me another way
              </button>
            </p>
          </>
        )}
        {step === "code" && sent && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(
                () =>
                  plainly(async () => {
                    await post("/v1/me/phone/verify", { code, ...(needsOld ? { old_code: oldCode } : {}) });
                    await refresh();
                    toast(`Your number is now ${showPhone(phone)}`);
                    onClose();
                  }),
                false,
              );
            }}
          >
            <p role="status">
              {sentWords(sent.sent_via, phone)}
              {needsOld && old && sent.old_sent_via ? ` And one to your old number, ${showPhone(old)}.` : ""}
            </p>
            <CodeField
              label="Code sent to your new number"
              code={code}
              setCode={setCode}
              left={left}
              busy={act.busy}
              sms={sent.sent_via !== "sms"}
              onResend={(via) => void act.run(() => start(phone, needsOld, via), false)}
            />
            {needsOld && (
              <Field label="Code sent to your old number">
                <input inputMode="numeric" autoComplete="off" maxLength={6} value={oldCode} onChange={(e) => setOldCode(e.target.value.replace(/\D/g, ""))} required />
              </Field>
            )}
            <ErrorBox error={act.error} />
            <button className="primary" type="submit" disabled={act.busy || code.length !== 6 || (needsOld && oldCode.length !== 6)}>
              {act.busy ? "Checking…" : "Confirm the new number"}
            </button>
          </form>
        )}
      </div>
    </Modal>
  );
}
