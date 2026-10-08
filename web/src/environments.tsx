import { get } from "./api";
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

/** The tenant's environments, in the order they were created. */
export function useEnvironments() {
  return useLoad(() => get<{ environments: Environment[] }>("/v1/environments"), []);
}

/** A select listing the tenant's environments. */
export function EnvSelect({ value, onChange, label = "Environment" }: { value: string; onChange: (env: string) => void; label?: string }) {
  const envs = useEnvironments();
  const names = envs.data?.environments.map((e) => e.name) ?? ["prod", "dev"];
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
