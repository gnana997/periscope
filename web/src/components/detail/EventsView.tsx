import { useObjectEvents } from "../../hooks/useResource";
import { ageFrom } from "../../lib/format";
import { cn } from "../../lib/cn";
import { DetailEmpty, DetailError, DetailLoading } from "./states";
import type { Event } from "../../lib/types";

interface EventsViewProps {
  cluster: string;
  kind: "pods" | "deployments" | "services" | "configmaps" | "namespaces";
  ns: string;
  name: string;
}

export function EventsView({ cluster, kind, ns, name }: EventsViewProps) {
  const { data, isLoading, isError, error } = useObjectEvents(
    cluster,
    kind,
    ns,
    name,
    true,
  );

  if (isLoading) return <DetailLoading label="loading events…" />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data || data.events.length === 0)
    return <DetailEmpty label="no events for this object" />;

  return (
    <ul className="divide-y divide-border">
      {data.events.map((ev, i) => (
        <li key={i} className="px-5 py-3">
          <EventRow event={ev} />
        </li>
      ))}
    </ul>
  );
}

function EventRow({ event }: { event: Event }) {
  const isWarning = event.type === "Warning";
  return (
    <div className="flex gap-3">
      <span
        className={cn(
          "mt-1 block size-1.5 shrink-0 rounded-full",
          isWarning ? "bg-red" : "bg-green",
        )}
      />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span
            className={cn(
              "font-mono text-[12px] font-medium",
              isWarning ? "text-red" : "text-ink",
            )}
          >
            {event.reason}
          </span>
          <span className="text-[11px] text-ink-faint">·</span>
          <span className="text-[11.5px] text-ink-muted">
            {ageFrom(event.last)} ago
          </span>
          {event.count > 1 && (
            <>
              <span className="text-[11px] text-ink-faint">·</span>
              <span className="font-mono text-[11.5px] text-ink-muted">
                ×{event.count}
              </span>
            </>
          )}
          {event.source && (
            <>
              <span className="text-[11px] text-ink-faint">·</span>
              <span className="font-mono text-[11.5px] text-ink-faint">
                {event.source}
              </span>
            </>
          )}
        </div>
        <div className="mt-1 text-[12px] leading-relaxed text-ink">
          {event.message}
        </div>
      </div>
    </div>
  );
}
