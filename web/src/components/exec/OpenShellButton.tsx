import { useEffect, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import { usePodDetail } from "../../hooks/useResource";
import { useExecSessions } from "../../exec/ExecSessionsContext";
import { CapReachedDialog } from "../../exec/CapReachedDialog";

/**
 * OpenShellButton — pod-detail action that opens (or focuses) a shell
 * session in the global drawer.
 *
 * UX:
 *   - Single-container pod: simple button.
 *   - Multi-container pod: button + dropdown chevron. The button opens
 *     the default container; the chevron reveals the picker.
 *
 * The default container is the value of the
 * kubectl.kubernetes.io/default-container annotation on the pod, falling
 * back to the first non-init container with state=Running, falling back
 * to the first non-init container.
 */

const DEFAULT_CONTAINER_ANNOTATION =
  "kubectl.kubernetes.io/default-container";

interface OpenShellButtonProps {
  cluster: string;
  namespace: string;
  pod: string;
}

export function OpenShellButton({
  cluster,
  namespace,
  pod,
}: OpenShellButtonProps) {
  const { data: detail } = usePodDetail(cluster, namespace, pod);
  const { openSession } = useExecSessions();

  const [pickerOpen, setPickerOpen] = useState(false);
  const [capReached, setCapReached] = useState(false);
  const wrapperRef = useRef<HTMLDivElement | null>(null);

  // Close the picker on outside click.
  useEffect(() => {
    if (!pickerOpen) return;
    function onClick(e: MouseEvent) {
      if (!wrapperRef.current) return;
      if (!wrapperRef.current.contains(e.target as Node)) {
        setPickerOpen(false);
      }
    }
    window.addEventListener("mousedown", onClick);
    return () => window.removeEventListener("mousedown", onClick);
  }, [pickerOpen]);

  const containers = detail?.containers ?? [];
  const defaultContainerName = (() => {
    const anno = detail?.annotations?.[DEFAULT_CONTAINER_ANNOTATION];
    if (anno && containers.some((c) => c.name === anno)) return anno;
    const running = containers.find((c) => c.state === "Running");
    if (running) return running.name;
    return containers[0]?.name ?? "";
  })();

  function open(containerName: string) {
    setPickerOpen(false);
    const result = openSession({
      cluster,
      namespace,
      pod,
      // Empty string lets the server resolve when our heuristic finds
      // nothing (race conditions during pod startup, etc.).
      container: containerName || undefined,
    });
    if (!result.ok && result.reason === "cap_reached") {
      setCapReached(true);
    }
  }

  const multi = containers.length > 1;

  return (
    <>
      <div ref={wrapperRef} className="relative inline-flex h-7 items-stretch">
        <button
          type="button"
          onClick={() => open(defaultContainerName)}
          className={cn(
            "inline-flex items-center gap-1.5 border border-border bg-surface px-2 font-mono text-[11px] text-ink-muted transition-colors",
            "hover:border-accent/60 hover:bg-accent-soft hover:text-accent",
            multi ? "rounded-l-md border-r-0" : "rounded-md",
          )}
          title={
            multi
              ? `Open shell to ${defaultContainerName} (default)`
              : `Open shell`
          }
        >
          <ShellGlyph />
          <span>shell</span>
          {multi && defaultContainerName && (
            <span className="text-ink-faint">· {defaultContainerName}</span>
          )}
        </button>
        {multi && (
          <button
            type="button"
            onClick={() => setPickerOpen((v) => !v)}
            className={cn(
              "inline-flex items-center justify-center rounded-r-md border border-border bg-surface px-1.5 text-ink-muted transition-colors",
              "hover:border-accent/60 hover:bg-accent-soft hover:text-accent",
              pickerOpen && "border-accent/60 bg-accent-soft text-accent",
            )}
            aria-haspopup="listbox"
            aria-expanded={pickerOpen}
            title="Pick a container"
          >
            <svg
              width="9"
              height="9"
              viewBox="0 0 9 9"
              className={cn(
                "transition-transform",
                pickerOpen ? "rotate-180" : "rotate-0",
              )}
              aria-hidden
            >
              <path
                d="M2 3.5l2.5 2.5L7 3.5"
                stroke="currentColor"
                strokeWidth="1.4"
                fill="none"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          </button>
        )}

        {/* picker dropdown */}
        {pickerOpen && multi && (
          <div className="absolute right-0 top-[calc(100%+4px)] z-30 min-w-[200px] overflow-hidden rounded-md border border-border-strong bg-surface shadow-[0_12px_24px_-12px_rgba(0,0,0,0.3)]">
            <div className="border-b border-border bg-surface-2/50 px-2 py-1 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
              container
            </div>
            <ul role="listbox" className="py-0.5">
              {containers.map((c) => (
                <li key={c.name}>
                  <button
                    type="button"
                    role="option"
                    aria-selected={c.name === defaultContainerName}
                    onClick={() => open(c.name)}
                    className="flex w-full items-center gap-2 px-2 py-1.5 text-left font-mono text-[11.5px] transition-colors hover:bg-accent-soft hover:text-accent"
                  >
                    <span
                      aria-hidden
                      className={cn(
                        "block size-1.5 shrink-0 rounded-full",
                        c.ready ? "bg-green" : "bg-ink-faint/50",
                      )}
                    />
                    <span className="min-w-0 flex-1 truncate text-ink">
                      {c.name}
                    </span>
                    {c.name === defaultContainerName && (
                      <span className="text-[10px] text-ink-faint">
                        default
                      </span>
                    )}
                  </button>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>

      <CapReachedDialog open={capReached} onClose={() => setCapReached(false)} />
    </>
  );
}

function ShellGlyph() {
  return (
    <svg
      width="11"
      height="11"
      viewBox="0 0 11 11"
      aria-hidden
      className="text-ink-faint"
    >
      <path
        d="M2 3l2.5 2.5L2 8M5.5 8H9"
        stroke="currentColor"
        strokeWidth="1.3"
        fill="none"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
