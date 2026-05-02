import { useEffect, useRef, useState } from "react";
import { cn } from "../lib/cn";
import { useExecSessions } from "./useExecSessions";
import { usePodDetail } from "../hooks/useResource";
import type { ContainerStatus } from "../lib/types";
import type { ExecSessionMeta } from "./types";

/**
 * SessionFootbar — bottom strip of the drawer body for the active
 * session.
 *
 * Primary control: a dropdown that switches which container the shell
 * is opened against. Switching is implemented as
 * `closeSession(current) + openSession(same params with new container)`
 * — no confirm dialog, since selecting from a dropdown is itself the
 * explicit action and a fresh shell is the same outcome the operator
 * gets from Disconnect → reconnect today.
 *
 * Hidden when the pod has only one container — there's nothing to
 * switch to and an empty footbar would just consume terminal real
 * estate.
 *
 * Right side surfaces contextual readouts: container restart counter
 * and the image string. Useful glance-info while you're shelled in
 * ("oh, this is :v0.47.0 with 12 restarts — that explains the
 * crashloop").
 */

interface Props {
  session: ExecSessionMeta;
}

export function SessionFootbar({ session }: Props) {
  const { data: detail } = usePodDetail(
    session.cluster,
    session.namespace,
    session.pod,
  );

  const containers = detail?.containers ?? [];
  const initContainers = detail?.initContainers ?? [];

  // Don't render when there's nothing to switch to. The hook above
  // still fires (one extra GET per session open) but the UI is hidden;
  // since we already fetch pod detail elsewhere (OpenShellButton's
  // picker), react-query dedupes the request.
  if (containers.length <= 1) return null;

  return (
    <div className="flex h-7 shrink-0 items-center gap-3 border-t border-border bg-surface-2/40 px-3 font-mono text-[10.5px]">
      <ContainerPicker
        session={session}
        running={containers}
        init={initContainers}
      />
      <ContainerReadouts container={findContainer(containers, session.container)} />
    </div>
  );
}

function ContainerPicker({
  session,
  running,
  init,
}: {
  session: ExecSessionMeta;
  running: ContainerStatus[];
  init: ContainerStatus[];
}) {
  const { openSession, closeSession } = useExecSessions();
  const [open, setOpen] = useState(false);
  const wrapperRef = useRef<HTMLDivElement | null>(null);

  // Close on outside click.
  useEffect(() => {
    if (!open) return;
    function onClick(e: MouseEvent) {
      if (!wrapperRef.current) return;
      if (!wrapperRef.current.contains(e.target as Node)) setOpen(false);
    }
    window.addEventListener("mousedown", onClick);
    return () => window.removeEventListener("mousedown", onClick);
  }, [open]);

  function switchTo(name: string) {
    setOpen(false);
    if (name === session.container) return;
    // Fire the new session FIRST so the drawer's active tab flips to
    // the new one before the old client tears down — avoids a brief
    // empty-tab flash.
    openSession({
      cluster: session.cluster,
      namespace: session.namespace,
      pod: session.pod,
      container: name,
    });
    closeSession(session.id);
  }

  const current =
    findContainer(running, session.container) ??
    running[0]; // safety: shouldn't happen but render something

  return (
    <div ref={wrapperRef} className="relative flex items-center gap-1.5">
      <span className="text-ink-faint uppercase tracking-[0.06em]">
        container
      </span>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-haspopup="listbox"
        aria-expanded={open}
        className={cn(
          "flex h-5 items-center gap-1.5 rounded border border-border bg-surface px-1.5 text-ink transition-colors",
          "hover:border-border-strong",
          open && "border-border-strong bg-surface-2/60",
        )}
      >
        <StateDot state={current?.state} />
        <span>{session.container || "(resolving…)"}</span>
        <svg
          width="9"
          height="9"
          viewBox="0 0 9 9"
          aria-hidden
          className={cn(
            "text-ink-faint transition-transform",
            open ? "rotate-180" : "rotate-0",
          )}
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

      {open && (
        // Pop UP, not down — footbar sits at the very bottom of the
        // drawer so there's no room below.
        <div className="absolute bottom-[calc(100%+4px)] left-0 z-30 min-w-[260px] overflow-hidden rounded-md border border-border-strong bg-surface shadow-[0_-12px_24px_-12px_rgba(0,0,0,0.3)]">
          {init.length > 0 && (
            <ContainerSection
              title="init"
              containers={init}
              currentName={session.container}
              onPick={switchTo}
              disabled
            />
          )}
          <ContainerSection
            title="containers"
            containers={running}
            currentName={session.container}
            onPick={switchTo}
          />
        </div>
      )}
    </div>
  );
}

function ContainerSection({
  title,
  containers,
  currentName,
  onPick,
  disabled,
}: {
  title: string;
  containers: ContainerStatus[];
  currentName: string;
  onPick: (name: string) => void;
  disabled?: boolean;
}) {
  return (
    <>
      <div className="border-b border-border bg-surface-2/50 px-2 py-1 text-[9.5px] uppercase tracking-[0.08em] text-ink-faint">
        {title}
      </div>
      <ul role="listbox" className="py-0.5">
        {containers.map((c) => {
          const isCurrent = c.name === currentName;
          // Init containers can only be exec'd while running. In
          // practice they're almost always Terminated by the time
          // anyone opens the drawer, but we surface them anyway so
          // the operator knows they exist.
          const inactive =
            disabled || (c.state !== "Running" && c.state !== "Waiting");
          return (
            <li key={c.name}>
              <button
                type="button"
                role="option"
                aria-selected={isCurrent}
                disabled={inactive}
                onClick={() => onPick(c.name)}
                className={cn(
                  "flex w-full items-center gap-2 px-2 py-1.5 text-left font-mono text-[11px] transition-colors",
                  inactive
                    ? "cursor-not-allowed text-ink-faint"
                    : "text-ink hover:bg-accent-soft hover:text-accent",
                  isCurrent && !inactive && "bg-surface-2/40 text-ink",
                )}
              >
                <StateDot state={c.state} />
                <span className="min-w-0 flex-1 truncate">{c.name}</span>
                <span className="shrink-0 text-[10px] text-ink-faint">
                  {c.state}
                </span>
                {isCurrent && (
                  <span className="shrink-0 text-[10px] text-accent">
                    current
                  </span>
                )}
              </button>
            </li>
          );
        })}
      </ul>
    </>
  );
}

function ContainerReadouts({ container }: { container?: ContainerStatus }) {
  if (!container) return <span className="text-ink-faint">·</span>;
  return (
    <>
      <span className="text-ink-faint">·</span>
      <span className="text-ink-muted">
        restarts{" "}
        <span
          className={cn(
            "tabular-nums",
            container.restartCount > 5
              ? "text-red"
              : container.restartCount > 0
                ? "text-yellow"
                : "text-ink",
          )}
        >
          {container.restartCount}
        </span>
      </span>
      <span className="text-ink-faint">·</span>
      <span
        className="min-w-0 truncate text-ink-muted"
        title={container.image}
      >
        {container.image}
      </span>
    </>
  );
}

function StateDot({ state }: { state?: string }) {
  const tone =
    state === "Running"
      ? "bg-green"
      : state === "Waiting"
        ? "bg-yellow"
        : state === "Terminated"
          ? "bg-ink-faint/50"
          : "bg-ink-faint/40";
  return (
    <span
      aria-hidden
      className={cn("block size-1.5 shrink-0 rounded-full", tone)}
    />
  );
}

function findContainer(
  containers: ContainerStatus[],
  name: string,
): ContainerStatus | undefined {
  return containers.find((c) => c.name === name);
}

