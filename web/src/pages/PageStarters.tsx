import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { get, post } from "../api";
import { Icon } from "../icons";
import { buildStage, madeWords, partsWords, type BundleMade, type PageBuild, type PageStarter } from "../lib/pages_g5";
import { ErrorBox, Field, Modal, Skeleton, useAction, useLoad } from "../ui";
import { pageIcon } from "./Pages";

// Pages G5 (docs/pages.md): a page from a starter, with the table and the
// tools it brings, made and published together; and Describe it, a page
// drafted from one sentence, which says what it would make before anything
// is made. The server makes and checks all of it.

/** What was made, and the way to the page. */
function Made({ made, onClose }: { made: BundleMade; onClose: () => void }) {
  return (
    <div className="pg-made" role="status">
      {madeWords(made).map((w) => (
        <p key={w}>{w}</p>
      ))}
      <div className="toolbar">
        <Link className="button primary" to={`/pages/${made.page}`} onClick={onClose}>
          Open {made.title}
        </Link>
        {!made.published && (
          <Link className="button" to={`/pages/${made.page}/edit`} onClick={onClose}>
            Open the editor
          </Link>
        )}
      </div>
    </div>
  );
}

/** The starters: each a page with what it brings, said in plain words. */
export function StartersModal({ onClose }: { onClose: () => void }) {
  const list = useLoad(() => get<{ starters: PageStarter[] }>("/v1/page-starters"), []);
  const [picked, setPicked] = useState("");
  const [made, setMade] = useState<BundleMade>();
  const act = useAction();
  const chosen = list.data?.starters.find((s) => s.id === picked);
  return (
    <Modal title="Start from a starter" onClose={onClose}>
      {made ? (
        <Made made={made} onClose={onClose} />
      ) : list.error ? (
        <ErrorBox error={list.error} />
      ) : !list.data ? (
        <Skeleton rows={4} label="Loading the starters…" />
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!chosen) return;
            void act.run(async () => setMade(await post<BundleMade>(`/v1/page-starters/${chosen.id}/install`, { publish: true })), false);
          }}
        >
          <fieldset className="sf-choices">
            <legend>Which page?</legend>
            {list.data.starters.map((s) => (
              <label key={s.id} className="row" data-testid="page-starter">
                <input type="radio" name="starter" checked={picked === s.id} onChange={() => setPicked(s.id)} />
                <Icon name={pageIcon(s.icon)} />
                <span>
                  {s.title} <span className="hint">· {s.about}</span>
                </span>
              </label>
            ))}
          </fieldset>
          {chosen && (
            <div className="pg-brings" aria-label={`What ${chosen.title} brings`}>
              <p className="hint">The page has {partsWords(chosen.parts)}. It brings, made with it:</p>
              <ul>
                {chosen.makes.map((m) => (
                  <li key={m}>{m}</li>
                ))}
              </ul>
            </div>
          )}
          <ErrorBox error={act.error} />
          <div className="toolbar">
            <button className="primary" disabled={!chosen || act.busy}>
              {chosen ? `Add ${chosen.title}` : "Pick a page"}
            </button>
            <button type="button" onClick={onClose}>
              Cancel
            </button>
          </div>
        </form>
      )}
    </Modal>
  );
}

/** Describe it: one sentence, a draft read back with what it would make, then Make it. */
export function DescribePageModal({ onClose }: { onClose: () => void }) {
  const [goal, setGoal] = useState("");
  const [build, setBuild] = useState<PageBuild>();
  const [made, setMade] = useState<BundleMade>();
  const act = useAction();
  useEffect(() => {
    if (build?.status !== "running") return;
    const t = setTimeout(() => void get<PageBuild>(`/v1/page-builds/${build.id}`).then(setBuild, () => undefined), 1200);
    return () => clearTimeout(t);
  }, [build]);
  const review = build?.status === "proposed" ? build.review : undefined;
  const problems = build?.proposal?.problems ?? [];
  return (
    <Modal title="Describe it" onClose={onClose}>
      {made ? (
        <Made made={made} onClose={onClose} />
      ) : (
        <>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(async () => {
                const r = await post<{ id: string }>("/v1/page-builds", { goal });
                setBuild({ id: r.id, status: "running", stage: "queued", error: null, goal, page_name: null, proposal: null });
              }, false);
            }}
          >
            <Field label="What should the page show?" hint="One sentence, like: a page of open tickets with how many are waiting, and a button to add one">
              <textarea value={goal} rows={3} maxLength={4000} required onChange={(e) => setGoal(e.target.value)} />
            </Field>
            <div className="toolbar">
              <button className="primary" disabled={act.busy || build?.status === "running" || !goal.trim()}>
                {build ? "Draft it again" : "Draft the page"}
              </button>
            </div>
          </form>
          {build && buildStage(build) && (
            <p className={build.status === "failed" ? "error" : "hint"} role="status">
              {buildStage(build)}
            </p>
          )}
          {review && (
            <section className="pg-review" aria-label="The draft">
              <h3>{review.title}</h3>
              {build?.proposal?.summary && <p>{build.proposal.summary}</p>}
              <p className="hint">It has {partsWords(review.parts)}.</p>
              {review.makes.length > 0 && (
                <>
                  <h4>It will make</h4>
                  <ul>
                    {review.makes.map((m) => (
                      <li key={m}>{m}</li>
                    ))}
                  </ul>
                </>
              )}
              {review.uses.length > 0 && (
                <>
                  <h4>It uses what you have</h4>
                  <ul>
                    {review.uses.map((m) => (
                      <li key={m}>{m}</li>
                    ))}
                  </ul>
                </>
              )}
              {problems.length > 0 ? (
                <div className="warning" role="alert">
                  It cannot be made as it is: {problems[0]?.message}
                </div>
              ) : (
                <div className="toolbar">
                  <button
                    className="primary"
                    disabled={act.busy}
                    onClick={() => void act.run(async () => setMade(await post<BundleMade>(`/v1/page-builds/${build?.id}/make`, { publish: true })), false)}
                  >
                    Make it
                  </button>
                </div>
              )}
            </section>
          )}
          <ErrorBox error={act.error} />
        </>
      )}
    </Modal>
  );
}
