import { useState } from "react";
import { del, get, post } from "../api";
import { Badge, ErrorBox, Field, Skeleton, useAction, useLoad } from "../ui";
import { pageEmbedSnippet, siteState, type EmbedSite } from "../lib/embedsites";
import { recordSteps, registrarOf, REGISTRARS } from "../lib/registrars";

// "On your website" (no-technical-steps N9): the business's page shown on
// a website it already has. The site is proven with a DNS record or a
// file; whoever looks after the website can be sent both, with the code,
// by email.

export function StorefrontEmbedSites({ pageURL, business }: { pageURL: string; business: string }) {
  const list = useLoad(() => get<{ sites: EmbedSite[]; max: number }>("/v1/embed-sites"), []);
  const [site, setSite] = useState("");
  const [method, setMethod] = useState<"dns" | "file">("dns");
  const [notice, setNotice] = useState("");
  const act = useAction();
  const sites = list.data?.sites ?? [];
  const full = !!list.data && sites.length >= list.data.max;
  const done = (msg: string) => (setNotice(msg), list.reload());
  return (
    <section className="card" aria-labelledby="sf-embed">
      <h2 id="sf-embed">On your website</h2>
      <p className="hint">Already have a website? Show your page on it: your catalogue, orders and bookings. Add the website, prove it is yours, then put the code on it.</p>
      <ErrorBox error={list.error ?? act.error} />
      {notice && (
        <p className="hint" role="status">
          {notice}
        </p>
      )}
      {list.data === undefined && !list.error && <Skeleton rows={2} label="Loading your websites…" />}
      {sites.map((s) => (
        <SiteCard
          key={s.id}
          s={s}
          pageURL={pageURL}
          business={business}
          onChange={done}
          onRemove={() =>
            confirm(`Stop showing your page on ${s.origin}?`) &&
            void act.run(async () => {
              await del(`/v1/embed-sites/${s.id}`);
              done(`${s.origin} was removed.`);
            }, false)
          }
        />
      ))}
      {!full && list.data && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              const s = await post<EmbedSite>("/v1/embed-sites", { origin: site, method });
              setSite("");
              done(`Added ${s.origin}. Prove it is yours as shown below.`);
            }, false);
          }}
        >
          <Field label="Your website" hint="Its address, such as adakitchen.ng.">
            <input value={site} onChange={(e) => setSite(e.target.value)} placeholder="adakitchen.ng" autoComplete="off" spellCheck={false} />
          </Field>
          <fieldset>
            <legend>How to prove it is yours</legend>
            <label className="choice">
              <input type="radio" name="embed-method" checked={method === "dns"} onChange={() => setMethod("dns")} /> I can add a record where I bought the domain
            </label>
            <label className="choice">
              <input type="radio" name="embed-method" checked={method === "file"} onChange={() => setMethod("file")} /> Whoever built the website can upload a file to it
            </label>
          </fieldset>
          <button type="submit" disabled={act.busy || site.trim() === ""}>
            Add website
          </button>
        </form>
      )}
    </section>
  );
}

function SiteCard({ s, pageURL, business, onChange, onRemove }: { s: EmbedSite; pageURL: string; business: string; onChange: (msg: string) => void; onRemove: () => void }) {
  const [registrar, setRegistrar] = useState("");
  const [designer, setDesigner] = useState("");
  const act = useAction();
  const snippet = pageEmbedSnippet(pageURL, business);
  const reg = registrarOf(registrar);
  return (
    <div className="sf-domain" style={{ borderTop: "1px solid var(--border, #ddd)", paddingTop: 12, marginBottom: 12 }}>
      <div className="toolbar" style={{ marginBottom: 6 }}>
        <strong>{s.origin.replace(/^https:\/\//, "")}</strong>
        <Badge value={s.verified_at ? "live" : s.unverified_at ? "unverified" : "pending"} />
      </div>
      <p className="hint" style={{ marginTop: 0 }}>
        {siteState(s)}
      </p>
      {!s.verified_at && s.method === "dns" && (
        <>
          <Field label="Where did you buy the domain?">
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
              {reg.open.map((x) => (
                <li key={x}>{x}</li>
              ))}
              {recordSteps(reg, {
                type: "TXT",
                name: s.prove.record_name ?? "",
                short: s.prove.record_short ?? s.prove.record_name ?? "",
                value: s.prove.record_value ?? "",
                purpose: "proof",
              }).map((x) => (
                <li key={x}>{x}</li>
              ))}
            </ol>
          )}
        </>
      )}
      {!s.verified_at && s.method === "file" && (
        <p className="hint">
          Whoever looks after the website puts a file at {s.prove.file_url} with this line in it: {s.prove.file_contents}
        </p>
      )}
      <Field label="The code for your website" hint="Whoever looks after the website puts it where your page should show.">
        <textarea readOnly rows={3} value={snippet} aria-label="The code for your website" />
      </Field>
      <form
        className="toolbar"
        style={{ alignItems: "flex-end" }}
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            await post(`/v1/embed-sites/${s.id}/send-to-designer`, { email: designer });
            setDesigner("");
            onChange(`Sent to ${designer}: the code and how to prove the website is yours.`);
          }, false);
        }}
      >
        <Field label="Send to my web designer" hint="They get the code and the proof by email.">
          <input type="email" value={designer} onChange={(e) => setDesigner(e.target.value)} placeholder="designer@example.com" />
        </Field>
        <button type="submit" disabled={act.busy || !designer.includes("@")}>
          Send
        </button>
      </form>
      <ErrorBox error={act.error} />
      <div className="toolbar" style={{ marginBottom: 0 }}>
        {!s.verified_at && (
          <button
            disabled={act.busy}
            onClick={() =>
              void act.run(async () => {
                await post(`/v1/embed-sites/${s.id}/verify`);
                onChange(`${s.origin} now shows your page.`);
              }, false)
            }
          >
            Check now
          </button>
        )}
        <button className="danger" disabled={act.busy} onClick={onRemove}>
          Remove
        </button>
      </div>
    </div>
  );
}
