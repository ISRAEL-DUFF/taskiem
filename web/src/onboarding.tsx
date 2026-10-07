import { createContext, useContext, type ReactNode } from "react";
import { useLocation } from "react-router-dom";
import { get } from "./api";
import { HELP, helpLink, type HelpTopic, type Onboarding } from "./lib/onboarding";
import { useLoad } from "./ui";

// The tenant's getting-started state (GET /v1/onboarding), shared by the
// navigation, the Get started page and the help panels. It reloads as the
// person moves around, so a step done elsewhere shows as done.

interface Ctx {
  data: Onboarding | undefined;
  reload: () => void;
}

const OnboardingCtx = createContext<Ctx>({ data: undefined, reload: () => undefined });

export function OnboardingProvider({ children }: { children: ReactNode }) {
  const loc = useLocation();
  const { data, reload } = useLoad(() => get<Onboarding>("/v1/onboarding"), [loc.pathname]);
  return <OnboardingCtx.Provider value={{ data, reload }}>{children}</OnboardingCtx.Provider>;
}

export function useOnboarding(): Ctx {
  return useContext(OnboardingCtx);
}

/** A help panel: a few plain sentences, and the docs page when there is a
 * docs site. Closed by default. */
export function Help({ topic }: { topic: HelpTopic }) {
  const { data } = useOnboarding();
  const h = HELP[topic];
  const link = helpLink(data?.docs_url, h.doc);
  return (
    <details className="help card">
      <summary>{h.title}</summary>
      {h.body.map((p, i) => (
        <p key={i}>{p}</p>
      ))}
      {link && (
        <p>
          <a href={link} target="_blank" rel="noreferrer">
            Read more in the docs
          </a>
        </p>
      )}
    </details>
  );
}
