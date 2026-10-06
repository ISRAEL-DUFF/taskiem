import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { get, post, type Me } from "./api";

interface Auth {
  me: Me | null | undefined; // undefined while loading
  can: (perm: string) => boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
}

const Ctx = createContext<Auth | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  const refresh = useCallback(() => get<Me>("/v1/me").then(setMe, () => setMe(null)), []);
  useEffect(() => {
    void refresh();
    const onUnauthorised = () => setMe(null);
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
