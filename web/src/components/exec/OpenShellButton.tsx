import { useEffect, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import { usePodDetail } from "../../hooks/useResource";
import { useClusters } from "../../hooks/useClusters";
import { useCanI } from "../../hooks/useCanI";
import { useExecSessions } from "../../exec/useExecSessions";
import { CapReachedDialog } from "../../exec/CapReachedDialog";
import { Tooltip } from "../Tooltip";

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
  const { data: clustersData } = useClusters();
  const { openSession } = useExecSessions();

  // PR4: hide entirely when the operator has set `exec.enabled: false`
  // on this cluster. Treat "field absent" (older backend) as enabled —
  // forward-compatibility for a frontend deployed against a backend
  // that hasn't shipped the execEnabled bit.
  const clusterMeta = clustersData?.clusters.find((c) => c.name === cluster);
  const execDisabled = clusterMeta?.execEnabled === false;

  // RBAC gate via SAR. Disable (don't hide) when the user lacks
  // `create pods/exec`; the tooltip explains which tier or rule
  // would be needed.
  const canExec = useCanI(cluster, {
    verb: "create",
    resource: "pods",
    subresource: "exec",
    namespace,
    name: pod,
  });

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

  // Ctrl/Cmd-E shortcut while this button is mounted (i.e. while a pod
  // detail pane is open). Doesn't conflict with terminal Ctrl-E
  // (move-to-end-of-line) because the terminal is in the drawer body
  // and the open-shell shortcut only fires while the user is on the
  // pod page WITHOUT the terminal focused.
  //
  // Capture phase + stopPropagation match the Cmd-` shortcut wired in
  // ExecSessionsContext so the two coexist cleanly.
  useEffect(() => {
    if (execDisabled || !canExec.allowed) return;
    function onKey(e: KeyboardEvent) {
      const meta = e.metaKey || e.ctrlKey;
      if (!meta) return;
      if (e.key !== "e" && e.key !== "E" && e.code !== "KeyE") return;
      if (e.shiftKey || e.altKey) return;
      // Don't fire when focus is inside the terminal — that would
      // hijack the standard end-of-line shortcut. xterm parks focus
      // on a hidden textarea; we treat any textarea/input/contenteditable
      // as "leave it alone."
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.isContentEditable)
      ) {
        return;
      }
      e.preventDefault();
      e.stopPropagation();
      open(defaultContainerName);
    }
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
    // open() / defaultContainerName change with detail data; capturing
    // them in deps would re-bind the listener too aggressively. The
    // closure captures the latest values at fire time via the ref-y
    // pattern of recomputing inside the handler.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [execDisabled, canExec.allowed]);

  const multi = containers.length > 1;

  if (execDisabled) {
    // Hide rather than render a disabled stub: the action just doesn't
    // exist on this cluster and we don't want to clutter the detail
    // pane with greyed-out buttons (operators see only what they can
    // act on).
    return null;
  }

  const denied = !canExec.allowed;
  const allowedTitle = multi
    ? `Open shell to ${defaultContainerName} (default) · Ctrl-E`
    : `Open shell · Ctrl-E`;

  return (
    <>
      <div ref={wrapperRef} className="relative inline-flex h-7 items-stretch">
        <Tooltip content={denied ? canExec.tooltip : null}>
          <button
            type="button"
            onClick={() => !denied && open(defaultContainerName)}
            disabled={denied}
            aria-disabled={denied}
            className={cn(
              "inline-flex items-center gap-1.5 border border-border bg-surface px-2 font-mono text-[11px] text-ink-muted transition-colors",
              "hover:border-accent/60 hover:bg-accent-soft hover:text-accent",
              multi ? "rounded-l-md border-r-0" : "rounded-md",
              "disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:border-border disabled:hover:bg-surface disabled:hover:text-ink-muted",
            )}
            title={denied ? undefined : allowedTitle}
          >
            <ShellGlyph />
            <span>shell</span>
            {multi && defaultContainerName && (
              <span className="text-ink-faint">· {defaultContainerName}</span>
            )}
          </button>
        </Tooltip>
        {multi && (
          <button
            type="button"
            onClick={() => !denied && setPickerOpen((v) => !v)}
            disabled={denied}
            aria-disabled={denied}
            className={cn(
              "inline-flex items-center justify-center rounded-r-md border border-border bg-surface px-1.5 text-ink-muted transition-colors",
              "hover:border-accent/60 hover:bg-accent-soft hover:text-accent",
              pickerOpen && "border-accent/60 bg-accent-soft text-accent",
              "disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:border-border disabled:hover:bg-surface disabled:hover:text-ink-muted",
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
