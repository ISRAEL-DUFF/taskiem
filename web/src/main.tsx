import { StrictMode, Suspense, lazy } from "react";
import { createRoot } from "react-dom/client";
import "@fontsource-variable/inter";
import "@xyflow/react/dist/style.css";
import "./styles.css";
import "./motion.css";
import "./kit.css";
import "./chat.css";
import { App } from "./App";
import { Skeleton } from "./ui";
import { applyTheme, savedTheme } from "./theme";
import { applyBrand, savedBrand } from "./brand";
import { registerWorker } from "./lib/worker";

applyTheme(savedTheme());
applyBrand(savedBrand());
// The service worker: chat notifications, and "You're offline" (lib/worker.ts).
registerWorker();
// iOS Safari shows a tapped button's :active (the pressed look in
// styles.css) only when the page listens for touches.
document.addEventListener("touchstart", () => undefined, { passive: true });

// The operator console (/ops) is its own app with its own session; it
// never mounts the tenant console's providers (docs/operator-console.md).
const OpsApp = lazy(() => import("./ops/OpsApp").then((m) => ({ default: m.OpsApp })));
const ops = window.location.pathname === "/ops" || window.location.pathname.startsWith("/ops/");

createRoot(document.getElementById("root") as HTMLElement).render(
  <StrictMode>
    {ops ? (
      <Suspense fallback={<Skeleton />}>
        <OpsApp />
      </Suspense>
    ) : (
      <App />
    )}
  </StrictMode>,
);
