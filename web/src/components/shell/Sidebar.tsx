import { ResourceNav } from "./ResourceNav";

export function Sidebar() {
  return (
    <aside className="flex h-full w-[256px] shrink-0 flex-col bg-surface">
      <ResourceNav />
    </aside>
  );
}
