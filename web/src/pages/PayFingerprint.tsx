import { del, get } from "../api";
import { naira } from "../lib/invoices";
import { ErrorBox, Items, Row, fmtTime, useAction, useLoad } from "../ui";

/** A phone that pays by fingerprint (GET /v1/me/fingerprint). */
interface FingerprintDevice {
  id: string;
  device_id: string;
  name: string;
  platform: "android" | "ios";
  created_at: string;
  last_used_at?: string;
}

interface MeFingerprint {
  devices: FingerprintDevice[];
  one_kobo: number;
  day_kobo: number;
  left_today_kobo: number;
  proofs: string[];
}

/** Paying by fingerprint (docs/spec/fingerprint-key.md): a key on a phone,
 * behind its own fingerprint or face check, turned on in the app. The web
 * cannot use it (it pays with a passkey or the PIN); it lists the phones
 * it is on for, and removes them. */
export function FingerprintPhones() {
  const list = useLoad<MeFingerprint>(() => get<MeFingerprint>("/v1/me/fingerprint"), []);
  const act = useAction();
  const f = list.data;
  return (
    <section className="card" data-testid="fingerprint-phones">
      <h2 style={{ marginTop: 0 }}>Fingerprint on your phones</h2>
      <p className="hint">
        In the Taskiem app, a phone can pay with its own fingerprint or face check: up to{" "}
        {naira(f?.one_kobo ?? 50000000)} a payment and {naira(f?.day_kobo ?? 100000000)} a day. Turn it on in the
        app&rsquo;s Settings. Here you pay with your passkey or PIN.
      </p>
      <ErrorBox error={list.error ?? act.error} />
      {f && f.devices.length === 0 && <p className="hint">Not on for any phone.</p>}
      {f && f.devices.length > 0 && (
        <Items label="Phones that pay by fingerprint">
          {f.devices.map((d) => (
            <Row
              key={d.id}
              icon="fingerprint"
              title={d.name}
              sub={
                <span className="sub">
                  {d.platform === "ios" ? "iPhone" : "Android"} · turned on {fmtTime(d.created_at)}
                  {d.last_used_at && <> · last paid {fmtTime(d.last_used_at)}</>}
                </span>
              }
              testid="fingerprint-phone"
            >
              <button
                className="danger"
                disabled={act.busy}
                aria-label={`Remove ${d.name}`}
                onClick={() =>
                  confirm(`Stop paying by fingerprint on ${d.name}?`) &&
                  void act.run(async () => (await del(`/v1/me/fingerprint/${d.id}`), list.reload()), `${d.name} removed`)
                }
              >
                Remove
              </button>
            </Row>
          ))}
        </Items>
      )}
    </section>
  );
}
