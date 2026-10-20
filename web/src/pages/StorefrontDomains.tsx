import { useState } from "react";
import { del, get, post } from "../api";
import { Badge, ErrorBox, Field, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { domainHosts, domainState, recordsInOrder, tidyDomain, type DomainList, type StorefrontDomain } from "../lib/domains";

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
          onVerify={() =>
            void act.run(async () => {
              const v = await post<StorefrontDomain>(`/v1/storefront/domains/${d.id}/verify`);
              changed(v.primary ? `${v.domain} is live. Your page's address is now https://${v.domain}.` : `${v.domain} is live.`);
            })
          }
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

function DomainCard({ d, busy, onVerify, onPrimary, onRemove }: { d: StorefrontDomain; busy: boolean; onVerify: () => void; onPrimary: () => void; onRemove: () => void }) {
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
      )}
      {d.status === "live" && (
        <p className="hint">
          Keep the TXT record <code>_taskiem-verify</code>: it is checked again every few hours.
        </p>
      )}
      <div className="toolbar" style={{ marginTop: 8, marginBottom: 0 }}>
        {d.status !== "live" && (
          <button disabled={busy} onClick={onVerify}>
            Check now
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
