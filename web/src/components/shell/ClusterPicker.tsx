import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useClusters } from "../../hooks/useClusters";
import { cn } from "../../lib/cn";

export function ClusterPicker() {
  const { cluster: currentCluster, resource } = useParams();
  const { data, isLoading, isError } = useClusters();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const wrapperRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const clusters = data?.clusters ?? [];
  const active = clusters.find((c) => c.name === currentCluster);

  if (isLoading) {
    return (
      <div className="mx-3 my-1 h-12 rounded-md border border-border bg-surface-2/40" />
    );
  }
  if (isError || clusters.length === 0) {
    return (
      <div className="mx-3 my-1 rounded-md border border-border bg-surface px-3 py-2">
        <div className="text-[11px] uppercase tracking-[0.06em] text-ink-faint">
          Cluster
        </div>
        <div className="mt-1 text-xs text-red">No clusters configured</div>
      </div>
    );
  }

  return (
    <div ref={wrapperRef} className="relative mx-3 my-1">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={cn(
          "flex w-full items-center gap-2.5 rounded-md border border-border bg-surface px-3 py-2 text-left transition-colors",
          "hover:border-border-strong",
          open && "border-border-strong",
        )}
      >
        <span className="block size-1.5 shrink-0 rounded-full bg-green" />
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-[12.5px] font-medium text-ink">
            {active?.name ?? "Select cluster"}
          </div>
          <div className="truncate text-[10.5px] text-ink-muted">
            {active?.backend === "kubeconfig"
              ? `kubeconfig · ${active.kubeconfigContext ?? "default"}`
              : active?.region
                ? `eks · ${active.region}`
                : active?.backend ?? "—"}
          </div>
        </div>
        <Chevron open={open} />
      </button>

      {open && (
        <div className="absolute inset-x-0 top-[calc(100%+4px)] z-20 overflow-hidden rounded-md border border-border-strong bg-surface shadow-[0_8px_28px_rgba(0,0,0,0.12)] dark:shadow-[0_8px_28px_rgba(0,0,0,0.5)]">
          <div className="max-h-[280px] overflow-auto py-1">
            {clusters.map((c) => {
              const isActive = c.name === currentCluster;
              return (
                <button
                  key={c.name}
                  type="button"
                  onClick={() => {
                    setOpen(false);
                    navigate(`/clusters/${c.name}/${resource ?? "pods"}`);
                  }}
                  className={cn(
                    "flex w-full items-center gap-2.5 px-3 py-2 text-left transition-colors",
                    isActive ? "bg-accent-soft" : "hover:bg-surface-2",
                  )}
                >
                  <span className="block size-1.5 shrink-0 rounded-full bg-green" />
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-mono text-[12.5px] font-medium text-ink">
                      {c.name}
                    </div>
                    <div className="truncate text-[10.5px] text-ink-muted">
                      {c.backend === "kubeconfig"
                        ? `kubeconfig · ${c.kubeconfigContext ?? "default"}`
                        : `eks · ${c.region}`}
                    </div>
                  </div>
                  <span className="text-[9.5px] uppercase tracking-[0.06em] text-ink-faint">
                    {c.backend === "kubeconfig" ? "kubeconfig" : "eks"}
                  </span>
                </button>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

function Chevron({ open }: { open: boolean }) {
  return (
    <svg
      width="10"
      height="10"
      viewBox="0 0 10 10"
      className={cn(
        "shrink-0 text-ink-faint transition-transform",
        open && "rotate-180",
      )}
      aria-hidden
    >
      <path
        d="M2 4l3 3 3-3"
        stroke="currentColor"
        strokeWidth="1.3"
        fill="none"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
