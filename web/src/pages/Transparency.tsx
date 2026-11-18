// The published count of lawful requests (decision 0069 point 11;
// docs/spec/people-chatting.md §8.5): for anyone, signed in or out. Only
// numbers, per half-year; linked from the privacy pages.

import { Link } from "react-router-dom";
import { get } from "../api";
import { periodWords, withCurrentHalf, type LawfulPeriod } from "../lib/peoplesafety";
import { ErrorBox, Items, Row, Skeleton, useLoad } from "../ui";

/** /transparency. */
export function TransparencyPage() {
  const { data, error } = useLoad(() => get<{ periods: LawfulPeriod[]; words: string }>("/v1/transparency/lawful-requests"), []);
  const periods = data ? withCurrentHalf(data.periods) : [];
  return (
    <div className="login card transparency" data-testid="transparency">
      <h1>Requests from the authorities</h1>
      <p>
        {data?.words ??
          "Taskiem answers only proper legal process, gives the least that is asked, and tells the person where the law allows. These are the requests for people's data Taskiem received from authorities."}
      </p>
      <p className="hint">
        Chats between people are kept by Taskiem and can be opened by its servers; they are not end-to-end encrypted. No one at Taskiem reads them except a
        moderator handling a report of them, or to answer a request counted here, and each time is recorded.
      </p>
      <ErrorBox error={error} />
      {!data && !error && <Skeleton rows={2} label="Loading the figures…" />}
      {data && (
        <Items label="Requests by half-year">
          {periods.map((p) => (
            <Row key={`${p.year}-${p.half}`} icon="policies" title={p.label} testid="transparency-period" attrs={{ "data-received": String(p.received) }} sub={<span className="sub">{periodWords(p)}</span>} />
          ))}
        </Items>
      )}
      <p className="hint">A request still being looked at is counted as received only. The figures are updated as requests come in.</p>
      <Link className="button" to="/login">
        Back to Taskiem
      </Link>
    </div>
  );
}
