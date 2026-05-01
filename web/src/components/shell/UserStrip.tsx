import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { ThemeToggle } from "./ThemeToggle";

export function UserStrip() {
  const { data } = useQuery({
    queryKey: ["whoami"],
    queryFn: ({ signal }) => api.whoami(signal),
  });

  const actor = data?.actor ?? "—";
  const initials = (() => {
    if (!actor || actor === "—") return "·";
    const parts = actor.split(/[@.\s]/).filter(Boolean);
    if (parts.length === 0) return actor[0]?.toUpperCase() ?? "·";
    if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
    return (parts[0]![0] + parts[1]![0]).toUpperCase();
  })();

  return (
    <div className="flex items-center gap-2 border-t border-border px-3 py-3">
      <div
        className="flex size-7 shrink-0 items-center justify-center rounded-full bg-accent text-[11px] font-semibold text-white"
        aria-hidden
      >
        {initials}
      </div>
      <div className="min-w-0 flex-1">
        <div className="truncate text-[12px] text-ink">{actor}</div>
        <div className="truncate text-[10px] text-ink-faint">
          signed in via session
        </div>
      </div>
      <ThemeToggle />
    </div>
  );
}
