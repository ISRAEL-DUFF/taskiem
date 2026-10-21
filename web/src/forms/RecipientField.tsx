import { useState } from "react";
import { get, post } from "../api";
import { ErrorBox, useAction, useLoad } from "../ui";

// Paying a person or business by bank and account number
// (no-technical-steps N8): the bank is chosen by name, Taskiem reads the
// name on the account for the owner to confirm, and only then saves them
// in Paystack. The form keeps Paystack's code for them; nobody sees it.

interface Bank {
  name: string;
  code: string;
}

/** Digits only, at most 10: an account number as people type it. */
export function accountDigits(s: string): string {
  return s.replace(/\D/g, "").slice(0, 10);
}

/** The value a recipient field shows once chosen: who and where. */
export function recipientLine(name: string, bank: string, account: string): string {
  return `${name}, ${bank} ••••${account.slice(-4)}`;
}

export function RecipientField({ label, value, onChange }: { label: string; value: string; onChange: (code: string) => void }) {
  const banks = useLoad(() => get<{ banks: Bank[] }>("/v1/paystack/banks").then((r) => r.banks), []);
  const [bank, setBank] = useState("");
  const [account, setAccount] = useState("");
  const [found, setFound] = useState<string>();
  const [chosen, setChosen] = useState<string>();
  const act = useAction();
  const bankName = banks.data?.find((b) => b.code === bank)?.name ?? "";
  if (value && chosen) {
    return (
      <p role="status">
        Paying <strong>{chosen}</strong>{" "}
        <button type="button" className="link" onClick={() => (setChosen(undefined), setFound(undefined), onChange(""))}>
          Change
        </button>
      </p>
    );
  }
  if (banks.error !== undefined) {
    return <ErrorBox error={banks.error} />;
  }
  return (
    <div>
      <span className="toolbar">
        <select aria-label={`${label}: bank`} value={bank} onChange={(e) => (setBank(e.target.value), setFound(undefined))}>
          <option value="">Choose the bank…</option>
          {(banks.data ?? []).map((b) => (
            <option key={b.code} value={b.code}>
              {b.name}
            </option>
          ))}
        </select>
        <input
          aria-label={`${label}: account number`}
          inputMode="numeric"
          placeholder="10-digit account number"
          value={account}
          onChange={(e) => (setAccount(accountDigits(e.target.value)), setFound(undefined))}
        />
        {!found && (
          <button
            type="button"
            className="small"
            disabled={act.busy || !bank || account.length !== 10}
            onClick={() => void act.run(async () => setFound((await post<{ account_name: string }>("/v1/paystack/resolve", { bank_code: bank, account_number: account })).account_name))}
          >
            {act.busy ? "Checking…" : "Find the name"}
          </button>
        )}
      </span>
      {found && (
        <p role="status">
          The account is in the name of <strong>{found}</strong>. Is this who you mean to pay?{" "}
          <button
            type="button"
            className="small primary"
            disabled={act.busy}
            onClick={() =>
              void act.run(async () => {
                const r = await post<{ recipient_code: string }>("/v1/paystack/recipients", { bank_code: bank, account_number: account, name: found });
                setChosen(recipientLine(found, bankName, account));
                onChange(r.recipient_code);
              })
            }
          >
            Yes, pay them
          </button>{" "}
          <button type="button" className="link" onClick={() => (setFound(undefined), setAccount(""))}>
            No
          </button>
        </p>
      )}
      <ErrorBox error={act.error} />
    </div>
  );
}
