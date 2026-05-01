/**
 * Inline loading/error/empty states for the detail pane body.
 * Different from the full-page states in components/table/states.tsx —
 * these are scoped to the detail pane viewport.
 */

export function DetailLoading({ label }: { label?: string }) {
  return (
    <div className="flex h-full items-center justify-center px-5 py-10">
      <div className="flex items-center gap-2.5 text-[12px] text-ink-muted">
        <span
          aria-hidden
          className="block size-3 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent"
        />
        {label ?? "loading…"}
      </div>
    </div>
  );
}

export function DetailError({ message }: { message: string }) {
  return (
    <div className="px-5 py-5">
      <div className="rounded-md border border-red/40 bg-red-soft px-3 py-2.5 text-[12px]">
        <div className="font-medium text-red">couldn't load this view</div>
        <div className="mt-1 font-mono text-[11px] text-red/80 break-all">{message}</div>
      </div>
    </div>
  );
}

export function DetailEmpty({ label }: { label: string }) {
  return (
    <div className="flex h-full items-center justify-center px-5 py-10">
      <div className="text-[12.5px] text-ink-muted">{label}</div>
    </div>
  );
}
