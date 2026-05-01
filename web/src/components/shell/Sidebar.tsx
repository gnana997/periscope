import { Brand } from "./Brand";
import { ClusterPicker } from "./ClusterPicker";
import { ResourceNav } from "./ResourceNav";
import { UserStrip } from "./UserStrip";

export function Sidebar() {
  return (
    <aside className="flex h-full w-[256px] shrink-0 flex-col border-r border-border bg-surface">
      <Brand />
      <ClusterPicker />
      <div className="my-2 h-px bg-border" />
      <ResourceNav />
      <UserStrip />
    </aside>
  );
}
