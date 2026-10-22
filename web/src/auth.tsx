import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { get, post, type Me } from "./api";
import { passkeyLogin } from "./passkeys";
import { toast } from "./toast";

interface Auth {
  me: Me | null | undefined; // undefined while loading
  can: (perm: string) => boolean;
  login: (email: string, password: string) => Promise<void>;
  loginWithPasskey: () => Promise<void>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
}

const Ctx = createContext<Auth | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  const refresh = useCallback(() => get<Me>("/v1/me").then(setMe, () => setMe(null)), []);
  useEffect(() => {
    void refresh();
    const onUnauthorised = () =>
      setMe((was) => {
        if (was) toast("You were signed out. Sign in again to carry on.", "info");
        return null;
      });
    window.addEventListener("taskiem:unauthorised", onUnauthorised);
    return () => window.removeEventListener("taskiem:unauthorised", onUnauthorised);
  }, [refresh]);
  const value: Auth = {
    me,
    refresh,
    can: (perm) => !!me?.permissions.includes(perm),
    login: async (email, password) => {
      await post("/v1/auth/login", { email, password });
      await refresh();
    },
    loginWithPasskey: async () => {
      await passkeyLogin();
      await refresh();
    },
    logout: async () => {
      await post("/v1/auth/logout").catch(() => undefined);
      setMe(null);
    },
  };
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAuth(): Auth {
  const a = useContext(Ctx);
  if (!a) throw new Error("useAuth outside AuthProvider");
  return a;
}

/** Whether the business has Advanced on: the technical parts (expressions,
 * cron, JSON, a blank canvas, the dev environment) are shown only then. */
export function useAdvanced(): boolean {
  return !!useAuth().me?.advanced;
}

/** Inside a field agent's session in a business, the pages see what the
 * agent may do there rather than what they may do in their own workspace. */
export function AgentPermissions({ allowed, children }: { allowed: string[]; children: ReactNode }) {
  const outer = useAuth();
  const value: Auth = { ...outer, can: (perm) => allowed.includes(perm) };
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
