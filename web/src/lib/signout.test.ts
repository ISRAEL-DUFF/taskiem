import { describe, expect, it } from "vitest";
import { endSession, keepSigningOut } from "./signout";

const answer = (status: number) =>
  (async () => new Response(null, { status })) as unknown as typeof fetch;

describe("signing out", () => {
  it("is not done when the request never reaches the server", async () => {
    const dropped = (async () => {
      throw new TypeError("Failed to fetch");
    }) as unknown as typeof fetch;
    expect(await endSession(dropped)).toBe(false);
  });
  it("is not done when something on the way answers instead", async () => {
    expect(await endSession(answer(502))).toBe(false);
    expect(await endSession(answer(503))).toBe(false);
  });
  it("is done when the server ended the session, or says there is none", async () => {
    expect(await endSession(answer(204))).toBe(true);
    expect(await endSession(answer(401))).toBe(true);
  });
});

/** A window with a clock and a connection the test moves by hand. */
function fakeWindow() {
  let online: (() => void) | undefined;
  let timer: (() => void) | undefined;
  return {
    win: {
      addEventListener: (_: "online", fn: () => void) => void (online = fn),
      removeEventListener: () => void (online = undefined),
      setTimeout: (fn: () => void) => {
        timer = fn;
        return 1;
      },
      clearTimeout: () => void (timer = undefined),
    },
    comeOnline: () => online?.(),
    tick: () => {
      const t = timer;
      timer = undefined;
      t?.();
    },
    waiting: () => !!online || !!timer,
  };
}
const settle = () => new Promise((r) => setTimeout(r, 0));

describe("a sign-out that did not get through", () => {
  it("is tried again when the connection returns, until the server answers", async () => {
    const w = fakeWindow();
    const results = [false, false, true];
    let tries = 0;
    let done = 0;
    keepSigningOut(
      async () => results[tries++] ?? true,
      () => done++,
      w.win,
    );
    expect(tries).toBe(0);
    w.comeOnline();
    await settle();
    expect([tries, done]).toEqual([1, 0]);
    w.tick(); // still nothing: the next try is on the clock
    await settle();
    expect([tries, done]).toEqual([2, 0]);
    w.comeOnline();
    await settle();
    expect([tries, done]).toEqual([3, 1]);
    expect(w.waiting()).toBe(false);
    w.comeOnline();
    w.tick();
    await settle();
    expect([tries, done]).toEqual([3, 1]);
  });
  it("stops when told to", async () => {
    const w = fakeWindow();
    let tries = 0;
    const stop = keepSigningOut(
      async () => {
        tries++;
        return false;
      },
      () => undefined,
      w.win,
    );
    stop();
    w.comeOnline();
    w.tick();
    await settle();
    expect(tries).toBe(0);
  });
});
