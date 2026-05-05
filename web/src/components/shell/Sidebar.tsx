import { ResourceNav } from "./ResourceNav";
import { ApplyYamlEntry } from "../apply/ApplyYamlEntry";

export function Sidebar() {
  return (
    <aside className="flex h-full w-[256px] shrink-0 flex-col bg-surface">
      {/* Apply YAML — sits above the resource navigation as the global
          action affordance for "do something cluster-wide". Hidden
          for users without apply permission (useCanApply). */}
      <ApplyYamlEntry />
      <div className="mt-2 h-px bg-border" />
      <ResourceNav />
    </aside>
  );
}
