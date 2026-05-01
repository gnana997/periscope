import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

export interface DetailTab {
  id: string;
  label: string;
  /** false = renders as a SOON-badged disabled tab. */
  ready: boolean;
  /** Rendered when this tab is active. Only the active tab's content
   *  mounts, so each tab's queries are lazy. */
  content?: ReactNode;
}

interface DetailPaneProps {
  title: string;
  subtitle?: string;
  tabs: DetailTab[];
  activeTab: string;
  onTabChange: (id: string) => void;
  onClose: () => void;
}

/**
 * Generic detail pane shell. Doesn't fetch — pages compose tabs with
 * their own data-fetching components (Describe / YamlView / EventsView).
 */
export function DetailPane({
  title,
  subtitle,
  tabs,
  activeTab,
  onTabChange,
  onClose,
}: DetailPaneProps) {
  const active = tabs.find((t) => t.id === activeTab && t.ready);

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* Tab strip */}
      <div className="flex h-9 shrink-0 items-center gap-1 border-b border-border bg-surface px-3">
        {tabs.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => t.ready && onTabChange(t.id)}
            disabled={!t.ready}
            className={cn(
              "relative inline-flex items-center gap-1.5 px-2 py-1 text-[12px] transition-colors",
              !t.ready && "cursor-not-allowed text-ink-faint",
              t.ready && activeTab === t.id && "text-ink",
              t.ready && activeTab !== t.id && "text-ink-muted hover:text-ink",
            )}
          >
            {t.label}
            {!t.ready && (
              <span className="rounded-sm border border-border px-1 py-px text-[8.5px] uppercase tracking-[0.06em]">
                Soon
              </span>
            )}
            {t.ready && activeTab === t.id && (
              <span className="absolute inset-x-2 -bottom-px h-px bg-accent" />
            )}
          </button>
        ))}
        <div className="ml-auto">
          <button
            type="button"
            onClick={onClose}
            className="flex size-7 items-center justify-center rounded-md text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink"
            aria-label="Close detail"
          >
            <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
              <path
                d="M2 2l7 7M9 2l-7 7"
                stroke="currentColor"
                strokeWidth="1.4"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>
      </div>

      {/* Title row */}
      <div className="border-b border-border bg-surface px-5 py-3">
        <div className="flex items-baseline gap-3">
          <h2 className="truncate font-mono text-[14px] font-medium text-ink" title={title}>
            {title}
          </h2>
          {subtitle && (
            <>
              <span className="text-[11px] text-ink-faint">·</span>
              <span className="truncate font-mono text-[12px] text-ink-muted">{subtitle}</span>
            </>
          )}
        </div>
      </div>

      {/* Body */}
      <div className="flex-1 overflow-auto bg-surface">
        {active?.content}
      </div>
    </div>
  );
}
