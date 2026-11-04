import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { del, get, patch, post } from "../api";
import {
  EmptyState,
  ErrorBox,
  Field,
  Items,
  Modal,
  PageHeader,
  Row,
  Skeleton,
  useAction,
  useLoad,
} from "../ui";
import {
  frontNameProblem,
  frontSub,
  frontURL,
  type Front,
  type FrontsView,
} from "../lib/fronts";
import { pageURL, type Storefront } from "../lib/storefront";
import { StorefrontTabs } from "./Storefront";
import { FormCard } from "./StorefrontForms";

/** The storefront's fronts (docs/spec/fronts.md F3): the home page and
 * any other pages of things to sell or ask about, each with its own list
 * and forms. A front is added from a kind, off, and turned on when it has
 * what it needs. */
export function StorefrontFronts() {
  const view = useLoad(() => get<FrontsView>("/v1/storefront/fronts"), []);
  const sf = useLoad(() => get<Storefront>("/v1/storefront"), []);
  const [adding, setAdding] = useState(false);
  const act = useAction();
  const v = view.data;
  const published = (sf.data?.published_version ?? 0) > 0;
  const base = sf.data ? pageURL(sf.data) : "";
  return (
    <>
      <PageHeader
        title="Fronts"
        description="Your page, and more pages beside it: a menu for your kitchen, a page of questions. Each has its own list and its own forms."
        actions={
          v && (
            <button
              type="button"
              className="primary"
              onClick={() => setAdding(true)}
            >
              Add a front
            </button>
          )
        }
      />
      <StorefrontTabs />
      <ErrorBox error={view.error ?? sf.error ?? act.error} />
      {!v && !view.error && <Skeleton rows={3} label="Loading your fronts…" />}
      {v && (
        <section className="card" aria-labelledby="fronts-list">
          <h2 id="fronts-list" style={{ marginTop: 0 }}>
            Your fronts
          </h2>
          {v.fronts.length === 0 ? (
            <EmptyState icon="storefront">
              Set up your page first, under Page.
            </EmptyState>
          ) : (
            <Items label="Your fronts">
              {v.fronts.map((f) => (
                <Row
                  key={f.id}
                  letter={f.name}
                  title={
                    <Link to={`/storefront/fronts/${f.id}`}>{f.name}</Link>
                  }
                  side={
                    <span className={`badge ${f.enabled ? "ok" : "muted"}`}>
                      {f.enabled ? "On" : "Off"}
                    </span>
                  }
                  sub={<span className="sub">{frontSub(f, v.kinds)}</span>}
                  testid="front"
                >
                  {f.slug !== "" && (
                    <button
                      type="button"
                      className="small"
                      disabled={act.busy}
                      onClick={() =>
                        void act.run(
                          async () => (
                            await patch(`/v1/storefront/fronts/${f.id}`, {
                              enabled: !f.enabled,
                            }),
                            view.reload()
                          ),
                          f.enabled ? `${f.name} is off` : `${f.name} is on`,
                        )
                      }
                    >
                      {f.enabled ? "Turn off" : "Turn on"}
                      <span className="sr-only"> {f.name}</span>
                    </button>
                  )}
                  {published && f.enabled && f.slug !== "" && base && (
                    <a
                      className="button small"
                      href={frontURL(base, f)}
                      target="_blank"
                      rel="noreferrer"
                    >
                      Open<span className="sr-only"> {f.name}</span>
                    </a>
                  )}
                </Row>
              ))}
            </Items>
          )}
        </section>
      )}
      {adding && v && (
        <AddFront
          view={v}
          onClose={() => setAdding(false)}
          onAdded={(f) => {
            setAdding(false);
            view.reload();
            void f;
          }}
        />
      )}
    </>
  );
}

/** Add a front: its name and its kind, as cards with a line each. */
function AddFront({
  view,
  onClose,
  onAdded,
}: {
  view: FrontsView;
  onClose: () => void;
  onAdded: (f: Front) => void;
}) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState(view.kinds[0]?.name ?? "custom");
  const act = useAction();
  const navigate = useNavigate();
  const problem = frontNameProblem(name);
  return (
    <Modal title="Add a front" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            const f = await post<Front>("/v1/storefront/fronts", {
              name: name.trim(),
              kind,
            });
            onAdded(f);
            navigate(`/storefront/fronts/${f.id}`);
          }, false);
        }}
      >
        <Field
          label="Name"
          hint="As customers see it: Snacks, Small chops, Ask us"
        >
          <input
            value={name}
            maxLength={80}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <fieldset className="choices">
          <legend>What is it for?</legend>
          {view.kinds.map((k) => (
            <label key={k.name} className="choice">
              <input
                type="radio"
                name="front-kind"
                value={k.name}
                checked={kind === k.name}
                onChange={() => setKind(k.name)}
              />
              <span>
                <strong>{k.title}</strong>
                <span className="hint"> {k.line}</span>
              </span>
            </label>
          ))}
        </fieldset>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || problem !== ""}
          >
            {act.busy ? "Adding…" : "Add the front"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** One front: its name, line and address, whether it is on, its items and
 * its forms. */
export function StorefrontFront() {
  const { id = "" } = useParams();
  const view = useLoad(() => get<FrontsView>("/v1/storefront/fronts"), []);
  const sf = useLoad(() => get<Storefront>("/v1/storefront"), []);
  const f = view.data?.fronts.find((x) => x.id === id);
  const act = useAction();
  const navigate = useNavigate();
  const [edit, setEdit] = useState<{
    name: string;
    tagline: string;
    slug: string;
  } | null>(null);
  const published = (sf.data?.published_version ?? 0) > 0;
  const base = sf.data ? pageURL(sf.data) : "";
  const home = f?.slug === "";
  const form =
    edit ??
    (f ? { name: f.name, tagline: f.tagline ?? "", slug: f.slug } : null);
  const dirty =
    !!f &&
    !!edit &&
    (edit.name !== f.name ||
      edit.tagline !== (f.tagline ?? "") ||
      edit.slug !== f.slug);
  return (
    <>
      <PageHeader
        title={f?.name ?? "Front"}
        description={f ? (home ? "Your home page" : `/${f.slug}`) : undefined}
        actions={
          f &&
          !home && (
            <button
              type="button"
              className={f.enabled ? undefined : "primary"}
              disabled={act.busy}
              onClick={() =>
                void act.run(
                  async () => (
                    await patch(`/v1/storefront/fronts/${f.id}`, {
                      enabled: !f.enabled,
                    }),
                    view.reload()
                  ),
                  f.enabled ? `${f.name} is off` : `${f.name} is on`,
                )
              }
            >
              {f.enabled ? "Turn off" : "Turn on"}
            </button>
          )
        }
      />
      <StorefrontTabs />
      <p>
        <Link to="/storefront/fronts">All fronts</Link>
      </p>
      <ErrorBox error={view.error ?? sf.error ?? act.error} />
      {!view.data && !view.error && (
        <Skeleton rows={3} label="Loading the front…" />
      )}
      {view.data && !f && (
        <EmptyState icon="storefront">
          This front is not there any more.
        </EmptyState>
      )}
      {f && form && (
        <>
          <section className="card" aria-labelledby="front-about">
            <h2 id="front-about" style={{ marginTop: 0 }}>
              About this front
            </h2>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void act.run(
                  async () => (
                    await patch(`/v1/storefront/fronts/${f.id}`, {
                      name: form.name.trim(),
                      tagline: form.tagline.trim(),
                      ...(home ? {} : { slug: form.slug.trim() }),
                    }),
                    setEdit(null),
                    view.reload()
                  ),
                  "Saved",
                );
              }}
            >
              <Field label="Name">
                <input
                  value={form.name}
                  maxLength={80}
                  onChange={(e) => setEdit({ ...form, name: e.target.value })}
                />
              </Field>
              <Field
                label="Line under the name"
                hint="Optional: what customers find here"
              >
                <input
                  value={form.tagline}
                  maxLength={120}
                  onChange={(e) =>
                    setEdit({ ...form, tagline: e.target.value })
                  }
                />
              </Field>
              {!home && (
                <Field
                  label="Address"
                  hint={
                    base
                      ? `${base.replace(/\/+$/, "")}/${form.slug}`
                      : undefined
                  }
                >
                  <input
                    value={form.slug}
                    maxLength={40}
                    onChange={(e) =>
                      setEdit({ ...form, slug: e.target.value.toLowerCase() })
                    }
                  />
                </Field>
              )}
              <div className="toolbar">
                <button
                  type="submit"
                  className="primary"
                  disabled={act.busy || !dirty}
                >
                  Save
                </button>
              </div>
            </form>
          </section>
          <section className="card" aria-labelledby="front-items">
            <h2 id="front-items" style={{ marginTop: 0 }}>
              Items
            </h2>
            {home ? (
              <p className="hint">
                Your home page shows your catalogue: change it under{" "}
                <Link to="/storefront">Page</Link>.
              </p>
            ) : f.table_id ? (
              <p>
                {f.items === 0
                  ? "Nothing on this front yet. "
                  : `${f.items} on this front. `}
                <Link to={`/tables/${f.table_id}`}>
                  {f.items === 0 ? "Add your first item" : "Change the items"}
                </Link>
              </p>
            ) : (
              <p className="hint">
                This front has no list of items: it shows its forms.
              </p>
            )}
          </section>
          <section className="card sf-links" aria-labelledby="front-forms">
            <h2 id="front-forms" style={{ marginTop: 0 }}>
              Forms
            </h2>
            {f.actions.length === 0 ? (
              <EmptyState icon="forms">This front has no forms.</EmptyState>
            ) : (
              <div className="sf-forms">
                {f.actions.map((a) => (
                  <FormCard
                    key={`${a.id ?? a.kind}/${a.workflow_id ?? ""}`}
                    action={a}
                    previewURL={home ? base : frontURL(base, f)}
                    published={published}
                    onReload={view.reload}
                  />
                ))}
              </div>
            )}
          </section>
          {!home && (
            <div className="toolbar">
              <button
                type="button"
                className="danger"
                disabled={act.busy}
                onClick={() =>
                  confirm(
                    `Remove ${f.name}? A front with orders or a workflow is turned off instead.`,
                  ) &&
                  void act.run(async () => {
                    await del(`/v1/storefront/fronts/${f.id}`);
                    navigate("/storefront/fronts");
                  }, `${f.name} removed`)
                }
              >
                Remove this front
              </button>
            </div>
          )}
        </>
      )}
    </>
  );
}
