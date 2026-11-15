import type { PushOpen } from "../lib/pushOpen";

// What a tapped notification has to say on the page (docs/spec/mobile-app-m4.md
// N3): that it could not be opened here, or, for a link that was not a tap,
// whether to switch to the business it is for.
export function PushNotices({
  open,
  onSignOut,
}: {
  open: PushOpen;
  onSignOut: () => void;
}) {
  return (
    <>
      {open.problem && (
        <div className="notice" role="alert" data-testid="push-other-business">
          {open.problem}
          {open.signInThere && (
            <>
              {" "}
              <button type="button" className="small" onClick={onSignOut}>
                Sign out, to sign in there
              </button>
            </>
          )}
        </div>
      )}
      {open.ask && (
        <div
          className="notice"
          role="region"
          aria-label="Switch business"
          data-testid="push-ask-business"
        >
          <p style={{ margin: "0 0 8px" }}>
            This page is for {open.ask.name}. Switch to it?
          </p>
          <button type="button" className="primary small" onClick={open.accept}>
            Switch to {open.ask.name}
          </button>{" "}
          <button type="button" className="small" onClick={open.decline}>
            Stay here
          </button>
        </div>
      )}
    </>
  );
}
