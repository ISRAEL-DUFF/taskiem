import { useState } from "react";
import { del, get, post } from "../api";
import { useAuth } from "../auth";
import { diffLines, major, newestPerMajor, writeLines, type CatalogueEntry, type CatalogueVersion, type Diff, type Install } from "../lib/catalogue";
import { Badge, ErrorBox, Modal, PageHeader, fmtTime, useAction, useLoad } from "../ui";

/** The public connector catalogue: connectors other organisations publish, reviewed by Taskiem (docs/connector-submissions.md). */
export function Catalogue() {
  const { can } = useAuth();
  const manage = can("connector.manage");
  const catalogue = useLoad(() => get<{ connectors: CatalogueEntry[] }>("/v1/catalogue"), []);
  const installs = useLoad(() => get<{ installs: Install[] }>("/v1/catalogue/installs"), []);
  const [installing, setInstalling] = useState<CatalogueVersion | null>(null);
  const [upgrading, setUpgrading] = useState<Install | null>(null);
  const act = useAction();
  const reload = () => (catalogue.reload(), installs.reload());
  return (
    <>
      <PageHeader title="Connector catalogue" description="Ready-made connectors to other services that you can install and use in your workflows." />
      <p className="hint">
        Connectors published by other organisations, each version checked automatically and reviewed by Taskiem before it appears here. Installed versions are pinned: nothing changes until you upgrade, and an upgrade that reaches new hosts or makes new kinds of change asks you again.
      </p>
      <ErrorBox error={catalogue.error ?? installs.error ?? act.error} />

      {installs.data && installs.data.installs.length > 0 && (
        <section className="card">
          <h2>Installed</h2>
          <table>
            <thead>
              <tr>
                <th>Connector</th>
                <th>Version</th>
                <th>State</th>
                <th>Installed</th>
                {manage && <th><span className="sr-only">Actions</span></th>}
              </tr>
            </thead>
            <tbody>
              {installs.data.installs.map((i) => (
                <tr key={i.ref}>
                  <td>
                    <code>{i.ref}</code>
                  </td>
                  <td>{i.version}</td>
                  <td>
                    <Badge value={i.state} />
                    {i.revoke_reason && <div className="hint">Revoked: {i.revoke_reason}. Steps using it fail until you upgrade or uninstall.</div>}
                  </td>
                  <td>
                    {fmtTime(i.updated_at ?? i.installed_at)} by {i.installed_by}
                  </td>
                  {manage && (
                    <td>
                      {i.latest && <button onClick={() => setUpgrading(i)}>Upgrade to {i.latest}</button>}{" "}
                      <button
                        disabled={act.busy}
                        onClick={() => {
                          if (window.confirm(`Uninstall ${i.ref}? Workflows that use it stop validating.`)) void act.run(async () => (await del(`/v1/catalogue/installs/${i.connector}/${i.major}`), reload()));
                        }}
                      >
                        Uninstall
                      </button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      <section className="card" style={{ marginTop: 24 }}>
        <h2>Available</h2>
        {catalogue.data && catalogue.data.connectors.length === 0 && <div className="empty">Nothing is published yet.</div>}
        {catalogue.data?.connectors.map((c) => (
          <article key={c.id} className="card" style={{ marginBottom: 12 }} aria-label={c.name}>
            <h3 style={{ marginTop: 0 }}>
              {c.name} <span className="hint">by {c.publisher_name} (verified publisher)</span>
            </h3>
            <p>{c.description}</p>
            {newestPerMajor(c.versions).map((v) => {
              const installed = c.installed[major(v.version)];
              return (
                <div key={v.version} className="row" style={{ alignItems: "flex-start", gap: 24 }}>
                  <div className="grow">
                    <div>
                      <code>{v.ref}</code> {v.version} <span className="hint">{v.licence}</span>
                    </div>
                    <div className="hint">
                      Reaches {v.hosts.join(", ")}. Actions:{" "}
                      {Object.entries(v.actions)
                        .map(([a, cls]) => `${a} (${cls.replaceAll("_", " ")})`)
                        .join(", ")}
                      .
                    </div>
                  </div>
                  {installed ? (
                    <Badge value={`installed ${installed}`} />
                  ) : (
                    manage && (
                      <button className="primary" onClick={() => setInstalling(v)}>
                        Install {v.ref}
                      </button>
                    )
                  )}
                </div>
              );
            })}
          </article>
        ))}
      </section>
      {installing && <InstallDialog version={installing} onClose={() => setInstalling(null)} onDone={() => (setInstalling(null), reload())} />}
      {upgrading && <UpgradeDialog install={upgrading} onClose={() => setUpgrading(null)} onDone={() => (setUpgrading(null), reload())} />}
    </>
  );
}

function ConsentList({ hosts, writes }: { hosts: string[]; writes: string[] }) {
  return (
    <>
      <p>It will send your data, including the credentials you give it, to:</p>
      <ul>
        {hosts.map((h) => (
          <li key={h}>
            <code>{h}</code>
          </li>
        ))}
      </ul>
      {writes.length > 0 && (
        <>
          <p>and can make these changes at the provider:</p>
          <ul>
            {writes.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}

function InstallDialog({ version, onClose, onDone }: { version: CatalogueVersion; onClose: () => void; onDone: () => void }) {
  const [agreed, setAgreed] = useState(false);
  const act = useAction();
  return (
    <Modal title={`Install ${version.name} ${version.version}`} onClose={onClose}>
      <p className="hint">
        Published by {version.publisher_name}. Package digest <code title={version.digest}>{version.digest.slice(0, 16)}</code>.
      </p>
      <ConsentList hosts={version.consent.hosts} writes={writeLines(version.consent)} />
      <label className="row">
        <input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} /> I agree to these hosts and changes for this organisation
      </label>
      <ErrorBox error={act.error} />
      <div className="row" style={{ marginTop: 12 }}>
        <button
          className="primary"
          disabled={!agreed || act.busy}
          onClick={() => void act.run(async () => (await post("/v1/catalogue/installs", { connector: version.id, version: version.version, consent: version.consent }), onDone()))}
        >
          Install
        </button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}

function UpgradeDialog({ install, onClose, onDone }: { install: Install; onClose: () => void; onDone: () => void }) {
  const to = install.latest ?? install.version;
  const diff = useLoad(() => get<{ diff: Diff; version: CatalogueVersion }>(`/v1/catalogue/installs/${install.connector}/${install.major}/upgrade?to=${encodeURIComponent(to)}`), [install.ref, to]);
  const [agreed, setAgreed] = useState(false);
  const act = useAction();
  const d = diff.data?.diff;
  const v = diff.data?.version;
  return (
    <Modal title={`Upgrade ${install.ref} to ${to}`} onClose={onClose}>
      <ErrorBox error={diff.error ?? act.error} />
      {d && v && (
        <>
          <p>What changes from {d.from}:</p>
          <ul>
            {diffLines(d).map((l) => (
              <li key={l}>{l}</li>
            ))}
          </ul>
          {d.needs_consent && (
            <>
              <ConsentList hosts={v.consent.hosts} writes={writeLines(v.consent)} />
              <label className="row">
                <input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} /> I agree to these hosts and changes for this organisation
              </label>
            </>
          )}
          <div className="row" style={{ marginTop: 12 }}>
            <button
              className="primary"
              disabled={act.busy || (d.needs_consent && !agreed)}
              onClick={() => void act.run(async () => (await post(`/v1/catalogue/installs/${install.connector}/${install.major}/upgrade`, { version: to, consent: v.consent }), onDone()))}
            >
              Upgrade
            </button>
            <button onClick={onClose}>Cancel</button>
          </div>
        </>
      )}
    </Modal>
  );
}
