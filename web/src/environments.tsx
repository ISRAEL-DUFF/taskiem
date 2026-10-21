import { useMemo } from "react";
import { get } from "./api";
import { useAdvanced } from "./auth";
import { useLoad } from "./ui";

export interface Environment {
  name: string;
  promotion_from: string | null;
  git: boolean;
  workflows: number;
  created_at: string;
}

export interface Deployment {
  environment: string;
  version: number;
  deployed_by: string | null;
  promoted_from: string | null;
  deployed_at: string;
}

/** The environments a business without Advanced sees: production only (no-technical-steps N9). */
export function shownEnvironments(envs: Environment[], advanced: boolean): Environment[] {
  return advanced ? envs : envs.filter((e) => e.name === "prod");
}

/** The tenant's environments, in the order they were created; without Advanced, production alone. */
export function useEnvironments() {
  const advanced = useAdvanced();
  const l = useLoad(() => get<{ environments: Environment[] }>("/v1/environments"), []);
  const data = useMemo(() => l.data && { environments: shownEnvironments(l.data.environments, advanced) }, [l.data, advanced]);
  return { ...l, data };
}

/** A select listing the tenant's environments; nothing when there is only one to see. */
export function EnvSelect({ value, onChange, label = "Environment" }: { value: string; onChange: (env: string) => void; label?: string }) {
  const advanced = useAdvanced();
  const envs = useEnvironments();
  const names = envs.data?.environments.map((e) => e.name) ?? (advanced ? ["prod", "dev"] : ["prod"]);
  if (names.length <= 1 && value === (names[0] ?? "prod")) return null;
  return (
    <select aria-label={label} style={{ width: "auto" }} value={value} onChange={(e) => onChange(e.target.value)}>
      {names.map((n) => (
        <option key={n} value={n}>
          {n}
        </option>
      ))}
    </select>
  );
}
