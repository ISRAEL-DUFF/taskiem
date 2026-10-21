import { useState } from "react";
import { del, get, post } from "../api";
import { Badge, ErrorBox, Field, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { domainHosts, domainState, recordsInOrder, tidyDomain, type DomainCheck, type DomainList, type StorefrontDomain } from "../lib/domains";
import { recordSteps, registrarOf, REGISTRARS } from "../lib/registrars";

/** "Your own domain": a domain the business already owns, proven by a DNS
 * TXT record and then serving the storefront (docs/storefronts.md#your-own-domain). */
export function StorefrontDomains({ published, onChange }: { published: boolean; onChange: () => void }) {
  const list = useLoad(() => get<DomainList>("/v1/storefront/domains"), []);
  const [domain, setDomain] = useState("");
  const [notice, setNotice] = useState("");
  const act = useAction();
  const changed = (msg: string) => {
    setNotice(msg);
    list.reload();
    onChange();
  };
  const domains = list.data?.domains ?? [];
  const full = !!list.data && domains.length >= list.data.max;
  return (
    <section className="card" aria-labelledby="sf-domains">
      <h2 id="sf-domains">Your own domain</h2>
      <p className="hint">
        Already own a domain, such as adakitchen.ng? Serve your page on it. Add it here, create the records shown at the company you bought it from, then check it.
        {!published && " It shows your page once you publish it."}
      </p>
      <ErrorBox error={list.error ?? act.error} />
      {notice && (
        <p className="hint" role="status">
          {notice}
        </p>
      )}
      {list.data === undefined && !list.error && <Skeleton rows={2} label="Loading your domains…" />}
      {domains.map((d) => (
        <DomainCard
          key={d.id}
          d={d}
          busy={act.busy}
          onVerify={async () => {
            // Check now: say which record is missing; once the proof is there, serve the domain.
            const c = await post<DomainCheck>(`/v1/storefront/domains/${d.id}/check`);
            if (!c.proven) return c;
            const v = await post<StorefrontDomain>(`/v1/storefront/domains/${d.id}/verify`);
            changed(v.primary ? `${v.domain} is live. Your page's address is now https://${v.domain}.` : `${v.domain} is live.`);
            return c;
          }}
          onPrimary={() =>
            void act.run(async () => {
              await post(`/v1/storefront/domains/${d.id}/primary`);
              changed(`${d.domain} is now your page's address.`);
            })
          }
          onRemove={() =>
            confirm(`Stop serving your page on ${d.domain}?`) &&
            void act.run(async () => {
              await del(`/v1/storefront/domains/${d.id}`);
              changed(`${d.domain} was removed.`);
            })
          }
        />
      ))}
      {!full && list.data && (
        <form
          className="toolbar"
          style={{ alignItems: "flex-end", marginBottom: 0 }}
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              const d = await post<StorefrontDomain>("/v1/storefront/domains", { domain });
              setDomain("");
              changed(`Added ${d.domain}. Create the records below, then check it.`);
            });
          }}
        >
          <Field label="Domain" hint="Your domain, or a part of it such as shop.example.com. Adding adakitchen.ng covers www.adakitchen.ng too.">
            <input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="adakitchen.ng" autoComplete="off" spellCheck={false} />
          </Field>
          <button type="submit" disabled={act.busy || tidyDomain(domain) === ""}>
            Add domain
          </button>
        </form>
      )}
    </section>
  );
}

function DomainCard({
  d,
  busy,
  onVerify,
  onPrimary,
  onRemove,
}: {
  d: StorefrontDomain;
  busy: boolean;
  onVerify: () => Promise<DomainCheck>;
  onPrimary: () => void;
  onRemove: () => void;
}) {
  const [registrar, setRegistrar] = useState("");
  const [check, setCheck] = useState<DomainCheck>();
  const checking = useAction();
  const reg = registrarOf(registrar);
  return (
    <div className="sf-domain" style={{ borderTop: "1px solid var(--border, #ddd)", paddingTop: 12, marginBottom: 12 }}>
      <div className="toolbar" style={{ marginBottom: 6 }}>
        <strong>{domainHosts(d).join(" and ")}</strong>
        <Badge value={d.status} />
        {d.primary && d.status === "live" && <Badge value="primary" />}
      </div>
      <p className="hint" style={{ marginTop: 0 }}>
        {domainState(d)}
        {d.checked_at && ` Last checked ${fmtTime(d.checked_at)}.`}
      </p>
      {d.status !== "live" && (
        <div className="sf-guide">
          <Field label={`Where did you buy ${d.domain}?`}>
            <select value={registrar} onChange={(e) => setRegistrar(e.target.value)}>
              <option value="">Choose the company…</option>
              {REGISTRARS.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </select>
          </Field>
          {registrar && (
            <ol>
              {reg.open.map((s) => (
                <li key={s}>{s}</li>
              ))}
              {recordsInOrder(d).map((r, i) => (
                <li key={`${r.type}/${r.name}/${r.value}`}>
                  {i === 0 ? "First, the record that proves the domain is yours:" : `Then the record that sends visitors to your page (${r.short ?? r.name}):`}
                  <ul>
                    {recordSteps(reg, r).map((s) => (
                      <li key={s}>{s}</li>
                    ))}
                  </ul>
                  <Copy text={r.value} label={`Copy the value for ${r.short ?? r.name}`} />
                </li>
              ))}
              <li>Come back here and press Check now. It can take from a few minutes to a few hours for the records to show.</li>
            </ol>
          )}
        </div>
      )}
      {check && d.status !== "live" && (
        <ul className="sf-checks" role="status">
          {check.checks.map((c) => (
            <li key={`${c.type}/${c.name}`}>
              {c.ok ? "✓" : "✗"} {c.says}
            </li>
          ))}
        </ul>
      )}
      <ErrorBox error={checking.error} />
      {d.status !== "live" && (
        <details>
          <summary>The records as a table</summary>
        <table>
          <thead>
            <tr>
              <th>Type</th>
              <th>Name</th>
              <th>Value</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {recordsInOrder(d).map((r) => (
              <tr key={`${r.type}/${r.name}/${r.value}`}>
                <td>{r.type}</td>
                <td>
                  <code>{r.name}</code>
                </td>
                <td>
                  <code>{r.value}</code>
                  {r.note && <div className="hint">{r.note}</div>}
                </td>
                <td>
                  <Copy text={r.value} label={`Copy the ${r.type} value for ${r.name}`} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        </details>
      )}
      {d.status === "live" && (
        <p className="hint">
          Keep the TXT record <code>_taskiem-verify</code>: it is checked again every few hours.
        </p>
      )}
      <div className="toolbar" style={{ marginTop: 8, marginBottom: 0 }}>
        {d.status !== "live" && (
          <button disabled={busy || checking.busy} onClick={() => void checking.run(async () => setCheck(await onVerify()))}>
            {checking.busy ? "Checking…" : "Check now"}
          </button>
        )}
        {d.status === "live" && !d.primary && (
          <button disabled={busy} onClick={onPrimary}>
            Make main address
          </button>
        )}
        {d.status === "live" && (
          <a href={`https://${d.domain}`} target="_blank" rel="noreferrer">
            Open
          </a>
        )}
        <button className="danger" disabled={busy} onClick={onRemove}>
          Remove
        </button>
      </div>
    </div>
  );
}

function Copy({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className="small"
      aria-label={label}
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        });
      }}
    >
      {done ? "Copied" : "Copy"}
    </button>
  );
}
