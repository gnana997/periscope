import { cn } from "../../lib/cn";

export interface FilterStripProps {
  search: string;
  onSearch: (q: string) => void;

  statusFilter?: string | null;
  statusOptions?: string[];
  onStatusFilter?: (s: string | null) => void;

  resultCount?: number;
  totalCount?: number;
}

export function FilterStrip({
  search,
  onSearch,
  statusFilter,
  statusOptions,
  onStatusFilter,
  resultCount,
  totalCount,
}: FilterStripProps) {
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-border bg-bg px-6 py-2.5">
      <SearchInput value={search} onChange={onSearch} />
      {statusOptions && onStatusFilter && (
        <StatusPills
          options={statusOptions}
          value={statusFilter ?? null}
          onChange={onStatusFilter}
        />
      )}
      <div className="ml-auto font-mono text-[11px] text-ink-muted tabular">
        {typeof resultCount === "number" && typeof totalCount === "number" && (
          <>
            {resultCount}
            <span className="text-ink-faint"> / </span>
            {totalCount}
          </>
        )}
      </div>
    </div>
  );
}

function SearchInput({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="flex min-w-[240px] flex-1 items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-[12.5px] focus-within:border-border-strong">
      <svg
        width="13"
        height="13"
        viewBox="0 0 13 13"
        className="text-ink-faint"
        aria-hidden
      >
        <circle
          cx="5.5"
          cy="5.5"
          r="3.6"
          stroke="currentColor"
          strokeWidth="1.3"
          fill="none"
        />
        <path
          d="M8.3 8.3l2.4 2.4"
          stroke="currentColor"
          strokeWidth="1.3"
          strokeLinecap="round"
        />
      </svg>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="filter by name"
        className="min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink outline-none placeholder:text-ink-faint"
      />
    </div>
  );
}

function StatusPills({
  options,
  value,
  onChange,
}: {
  options: string[];
  value: string | null;
  onChange: (s: string | null) => void;
}) {
  const all = ["All", ...options];
  return (
    <div className="flex items-center gap-1 rounded-md border border-border bg-surface p-1">
      {all.map((s) => {
        const active = (s === "All" && value === null) || s === value;
        return (
          <button
            key={s}
            type="button"
            onClick={() => onChange(s === "All" ? null : s)}
            className={cn(
              "rounded px-2 py-0.5 text-[11.5px] transition-colors",
              active
                ? "bg-accent-soft text-accent"
                : "text-ink-muted hover:text-ink",
            )}
          >
            {s.toLowerCase()}
          </button>
        );
      })}
    </div>
  );
}
