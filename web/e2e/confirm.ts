import { execFileSync } from "node:child_process";

// A member an owner adds with a password confirms their email before they
// add a passkey or authenticator, or link a chat (F9: an address no one
// has confirmed could be someone else's). Tests stand in for the link in
// their inbox by marking the address confirmed in the e2e database.
export function confirmEmail(email: string): void {
  const base = process.env.TASKIEM_TEST_DATABASE_URL;
  if (!base) throw new Error("set TASKIEM_TEST_DATABASE_URL");
  execFileSync("psql", [base.replace(/\/[^/?]*(\?.*)?$/, "/taskiem_e2e$1"), "-v", "ON_ERROR_STOP=1", "-q", "-v", `email=${email}`], {
    input: "UPDATE users SET email_verified_at = now() WHERE lower(email) = lower(:'email') AND email_verified_at IS NULL;\n",
    encoding: "utf8",
  });
}
