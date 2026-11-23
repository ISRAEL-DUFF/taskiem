import { useState } from "react";
import { ApiError, get, patch, post } from "../api";
import { useAuth } from "../auth";
import { moneyAccess } from "../lib/money";
import {
  labelNameProblem,
  usesWords,
  type LabelsView,
  type SpendLabel,
} from "../lib/treat";
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
import { MoneyTabs } from "./Money";

// Money > Labels (what a business buys §5.4): the business's own labels
// for what it spends, most used first, renamed or merged into another by
// those who manage money. Past spending is never rewritten: reports group
// it by the label kept.

export function MoneyLabels() {
  const { can } = useAuth();
  const manage = moneyAccess(can).manage;
  const view = useLoad(() => get<LabelsView>("/v1/money/labels"), []);
  const [renaming, setRenaming] = useState<SpendLabel>();
  const [merging, setMerging] = useState<SpendLabel>();
  const labels = view.data?.labels;
  return (
    <>
      <PageHeader
        title="Labels"
        description="What you spend under. A label is made by typing it when you buy; rename one, or merge two that mean the same."
      />
      <MoneyTabs />
      {!manage ? (
        <p className="notice">
          Someone who manages money can rename and merge labels.
        </p>
      ) : (
        <>
          <p className="notice">
            Renaming or merging changes how your reports group spending, for
            all of it at once. What was recorded is never changed.
          </p>
          <ErrorBox error={view.error} />
          {!labels && !view.error && (
            <Skeleton rows={3} label="Loading your labels…" />
          )}
          {labels && labels.length === 0 && (
            <EmptyState icon="tag">
              No labels yet. The first is made when you say how to treat
              something you buy.
            </EmptyState>
          )}
          {labels && labels.length > 0 && (
            <Items label="Your labels">
              {labels.map((l) => (
                <Row
                  key={l.id}
                  letter={l.name}
                  title={l.name}
                  sub={<span className="sub">{usesWords(l.uses)}</span>}
                  testid="spend-label"
                >
                  <button
                    type="button"
                    className="small"
                    onClick={() => setRenaming(l)}
                  >
                    Rename<span className="sr-only"> {l.name}</span>
                  </button>
                  {labels.length > 1 && (
                    <button
                      type="button"
                      className="small"
                      onClick={() => setMerging(l)}
                    >
                      Merge<span className="sr-only"> {l.name}</span>
                    </button>
                  )}
                </Row>
              ))}
            </Items>
          )}
        </>
      )}
      {renaming && (
        <RenameModal
          label={renaming}
          labels={labels}
          onClose={() => setRenaming(undefined)}
          onSaved={() => {
            setRenaming(undefined);
            view.reload();
          }}
        />
      )}
      {merging && labels && (
        <MergeModal
          label={merging}
          labels={labels}
          onClose={() => setMerging(undefined)}
          onSaved={() => {
            setMerging(undefined);
            view.reload();
          }}
        />
      )}
    </>
  );
}

const plain = (e: unknown) => {
  if (e instanceof ApiError && e.status === 400)
    e.message = e.message.replace(/^[a-z]+: /, "");
  throw e;
};

function RenameModal({
  label,
  labels,
  onClose,
  onSaved,
}: {
  label: SpendLabel;
  labels: SpendLabel[] | undefined;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(label.name);
  const act = useAction();
  const problem = labelNameProblem(name, labels, label.id);
  return (
    <Modal title={`Rename ${label.name}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (
              await patch(`/v1/money/labels/${label.id}`, {
                name: name.trim(),
              }).catch(plain),
              onSaved()
            ),
            `Renamed to ${name.trim()}`,
          );
        }}
      >
        <Field label="New name">
          <input
            value={name}
            maxLength={60}
            onChange={(e) => setName(e.target.value)}
            autoFocus
          />
        </Field>
        {problem && name.trim() !== label.name && (
          <p className="hint">{problem}</p>
        )}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !!problem || name.trim() === label.name}
          >
            Rename
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

function MergeModal({
  label,
  labels,
  onClose,
  onSaved,
}: {
  label: SpendLabel;
  labels: SpendLabel[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const others = labels.filter((l) => l.id !== label.id);
  const [into, setInto] = useState(others[0]?.id ?? "");
  const act = useAction();
  const kept = others.find((l) => l.id === into);
  return (
    <Modal title={`Merge ${label.name}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (
              await post(`/v1/money/labels/${label.id}/merge`, { into }).catch(
                plain,
              ),
              onSaved()
            ),
            `${label.name} merged into ${kept?.name ?? "the label kept"}`,
          );
        }}
      >
        <Field
          label="Into"
          hint={`All spending under ${label.name}, past and to come, shows under the label you choose. ${label.name} is not offered again.`}
        >
          <select value={into} onChange={(e) => setInto(e.target.value)}>
            {others.map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
          </select>
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !into}
          >
            Merge
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
