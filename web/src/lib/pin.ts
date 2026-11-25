// The person's PIN (docs/spec/transaction-pin.md): four digits, not one
// repeated nor a run, checked here as the server checks it.

/** The PIN's length. */
export const PIN_DIGITS = 4;

/** A PIN that is one digit repeated or a run (1234, 9876), as the server
 * refuses. */
export function weakPin(pin: string): boolean {
  let same = true;
  let up = true;
  let down = true;
  for (let i = 1; i < pin.length; i++) {
    const d = pin.charCodeAt(i) - pin.charCodeAt(i - 1);
    same &&= d === 0;
    up &&= d === 1 || d === -9;
    down &&= d === -1 || d === 9;
  }
  return same || up || down;
}

/** What is wrong with the two PINs typed, before anything is sent. */
export function pinProblem(pin: string, again: string, digits = PIN_DIGITS): string | null {
  if (!new RegExp(`^[0-9]{${digits}}$`).test(pin)) return `Type the ${digits} digits of your new PIN.`;
  if (weakPin(pin)) return "Choose a PIN that is not one digit repeated or a run like 1234.";
  if (pin !== again) return "The two PINs are not the same.";
  return null;
}
