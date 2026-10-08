import { StrictMode, Suspense, lazy } from "react";
import { createRoot } from "react-dom/client";
import "@fontsource-variable/inter";
import "@xyflow/react/dist/style.css";
import "./styles.css";
import { App } from "./App";

// The operator console (/ops) is its own app with its own session; it
// never mounts the tenant console's providers (docs/operator-console.md).
const OpsApp = lazy(() => import("./ops/OpsApp").then((m) => ({ default: m.OpsApp })));
const ops = window.location.pathname === "/ops" || window.location.pathname.startsWith("/ops/");

createRoot(document.getElementById("root") as HTMLElement).render(
  <StrictMode>
    {ops ? (
      <Suspense fallback={<div className="empty">Loading…</div>}>
        <OpsApp />
      </Suspense>
    ) : (
      <App />
    )}
  </StrictMode>,
);
