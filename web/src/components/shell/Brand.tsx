export function Brand() {
  return (
    <div className="px-5 pt-5 pb-3">
      <div className="flex items-baseline gap-2">
        <h1
          className="font-display text-[22px] leading-none tracking-tight text-ink"
          style={{ fontWeight: 400 }}
        >
          Periscope
        </h1>
        <span className="text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          v0.1 · beta
        </span>
      </div>
    </div>
  );
}
