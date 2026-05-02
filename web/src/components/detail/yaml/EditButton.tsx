// EditButton — the small `[edit yaml]` button rendered by
// ResourceActions in the DetailPane tab strip. Click navigates the
// URL to the YAML tab in edit mode (?tab=yaml&edit=1); YamlView
// dispatches to the inline editor when the kind is in the registry
// and the user has patch permission.

import { useSearchParams } from "react-router-dom";

export function EditButton() {
  const [, setParams] = useSearchParams();

  const handleClick = () => {
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("tab", "yaml");
        next.set("edit", "1");
        return next;
      },
      { replace: true },
    );
  };

  return (
    <button
      type="button"
      onClick={handleClick}
      className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[12px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink"
    >
      edit yaml
    </button>
  );
}
