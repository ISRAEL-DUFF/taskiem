import { Link, useParams } from "react-router-dom";
import { get } from "../api";
import { dateLabel, naira } from "../lib/invoices";
import {
  termsWords,
  type Statement,
  type StatementSide,
} from "../lib/statements";
import {
  EmptyState,
  ErrorBox,
  Items,
  PageHeader,
  Row,
  Skeleton,
  useLoad,
} from "../ui";

// A linked business's statement (business links L5, §10): what they owe
// us (our open invoices to them) and what we owe them (their open bills),
// each with the terms and the credit limit, oldest due first. Both
// businesses' statements add up to the same numbers.

export function StatementPage() {
  const { id = "" } = useParams();
  const view = useLoad(
    () => get<Statement>(`/v1/links/${encodeURIComponent(id)}/statement`),
    [id],
  );
  const st = view.data;
  return (
    <>
      <PageHeader
        title={st ? `Statement with ${st.business}` : "Statement"}
        description="What is open between your two businesses, oldest due first. Theirs shows the same numbers."
        actions={
          <Link className="button" to={`/chat?with=${encodeURIComponent(id)}`}>
            Chat
          </Link>
        }
      />
      <ErrorBox error={view.error} />
      {!st && !view.error && (
        <Skeleton rows={3} label="Loading the statement…" />
      )}
      {st && !st.they_owe && !st.we_owe && (
        <EmptyState icon="invoices">
          Nothing is open between you and {st.business}.
        </EmptyState>
      )}
      {st?.they_owe && (
        <Side
          side={st.they_owe}
          title={`${st.business} owes you`}
          theirs={st.business}
          kind="invoice"
        />
      )}
      {st?.we_owe && (
        <Side
          side={st.we_owe}
          title={`You owe ${st.business}`}
          theirs={st.business}
          kind="bill"
        />
      )}
    </>
  );
}

function Side({
  side,
  title,
  theirs,
  kind,
}: {
  side: StatementSide;
  title: string;
  theirs: string;
  kind: "invoice" | "bill";
}) {
  const foot: [string, string][] = [];
  if (side.overdue_kobo > 0) foot.push(["Overdue", naira(side.overdue_kobo)]);
  if (side.oldest_due) foot.push(["Oldest due", dateLabel(side.oldest_due)]);
  if (side.terms_days !== undefined)
    foot.push(["Terms", termsWords(side.terms_days)]);
  if (side.credit_limit_kobo !== undefined)
    foot.push(["Credit limit", naira(side.credit_limit_kobo)]);
  if (side.available_kobo !== undefined)
    foot.push(["Left to use", naira(Math.max(0, side.available_kobo))]);
  const id = kind === "invoice" ? "they-owe" : "we-owe";
  return (
    <>
      <section
        className="hero"
        aria-labelledby={id}
        data-testid={`statement-${kind}`}
      >
        <div className="hero-top">
          <span className="hero-label" id={id}>
            {title}
          </span>
          {side.available_kobo !== undefined && side.available_kobo < 0 && (
            <span className="hero-tag">Over the limit</span>
          )}
        </div>
        <div className="hero-amount">{naira(side.owed_kobo)}</div>
        {foot.length > 0 && (
          <dl className="hero-foot">
            {foot.map(([k, v]) => (
              <div key={k}>
                <dt>{k}</dt>
                <dd>{v}</dd>
              </div>
            ))}
          </dl>
        )}
      </section>
      <section className="card" aria-labelledby={`${id}-docs`}>
        <h2 id={`${id}-docs`}>
          {kind === "invoice"
            ? `Your invoices to ${theirs}`
            : `${theirs}'s bills to you`}
        </h2>
        {side.documents.length === 0 ? (
          <p className="hint">None open.</p>
        ) : (
          <Items label={kind === "invoice" ? "Open invoices" : "Open bills"}>
            {side.documents.map((d) => (
              <Row
                key={d.id}
                icon="invoices"
                tone={d.overdue ? "warn" : d.disputed ? "danger" : "info"}
                title={
                  <Link
                    to={
                      kind === "invoice"
                        ? `/invoices/${d.id}`
                        : `/bills/${encodeURIComponent(d.reference)}`
                    }
                  >
                    {d.reference}
                  </Link>
                }
                side={<strong>{naira(d.due_kobo)}</strong>}
                sub={
                  <span className="sub">
                    {d.due_on
                      ? `${d.overdue ? "Was due" : "Due"} ${dateLabel(d.due_on)}`
                      : "No due date"}
                    {d.paid_kobo > 0
                      ? ` · ${naira(d.paid_kobo)} of ${naira(d.total_kobo)} paid`
                      : ""}
                    {d.disputed ? " · queried" : ""}
                  </span>
                }
                testid="statement-doc"
              />
            ))}
          </Items>
        )}
      </section>
    </>
  );
}
