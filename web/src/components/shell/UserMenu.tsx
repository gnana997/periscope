import { useEffect, useRef, useState } from "react";
import { useAuth } from "../../auth/useAuth";
import { cn } from "../../lib/cn";

/**
 * UserMenu — avatar in the cluster rail, click → popover with email,
 * mode badge, and sign-out actions.
 *
 * Replaces the old static UserAvatar. In dev mode the popover hides
 * the "everywhere" item (no Okta session to terminate); the mode
 * badge makes it obvious which credentials path is active.
 */
export function UserMenu() {
  const { user, signOut, signOutEverywhere } = useAuth();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDoc(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const label = user?.email ?? user?.subject ?? "—";
  const initials = userInitials(label);

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        title={label}
        aria-label={label}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "flex size-9 items-center justify-center rounded-full bg-accent text-[11px] font-semibold text-white transition-opacity",
          open ? "opacity-90" : "hover:opacity-90",
        )}
      >
        {initials}
      </button>

      {open ? (
        <div className="absolute left-full bottom-0 z-50 ml-2 w-[240px] rounded-md border border-border-strong bg-surface shadow-lg">
          <div className="border-b border-border px-3 py-2">
            <div className="truncate text-[12.5px] font-medium text-ink" title={label}>
              {label}
            </div>
            {user ? (
              <div className="mt-1 flex flex-wrap items-center gap-1 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
                <span
                  className={cn(
                    "rounded-sm border px-1 py-px",
                    user.mode === "oidc"
                      ? "border-accent/50 text-accent"
                      : "border-yellow/50 text-yellow",
                  )}
                  title={`auth backend: ${user.mode}`}
                >
                  {user.mode}
                </span>
                {user.tier ? (
                  <span
                    className={cn(
                      "rounded-sm border px-1 py-px",
                      tierClass(user.tier),
                    )}
                    title={`K8s tier: ${user.tier}`}
                  >
                    {user.tier}
                  </span>
                ) : user.authzMode === "tier" ? (
                  <span
                    className="rounded-sm border border-red/50 px-1 py-px text-red"
                    title="No tier resolved — defaultTier may be empty"
                  >
                    no tier
                  </span>
                ) : user.authzMode === "raw" ? (
                  <span
                    className="rounded-sm border border-ink-faint/50 px-1 py-px"
                    title="Raw impersonation: groups passed through with prefix"
                  >
                    raw
                  </span>
                ) : user.authzMode === "shared" ? (
                  <span
                    className="rounded-sm border border-ink-faint/50 px-1 py-px"
                    title="Shared mode: all users have the same K8s perms"
                  >
                    shared
                  </span>
                ) : null}
              </div>
            ) : null}
            {user && user.groups.length > 0 ? (
              <div
                className="mt-1 truncate font-mono text-[10px] text-ink-faint"
                title={user.groups.join(", ")}
              >
                {user.groups.join(", ")}
              </div>
            ) : null}
          </div>

          <ul className="py-1">
            <MenuItem
              onClick={() => {
                setOpen(false);
                signOut();
              }}
            >
              sign out
            </MenuItem>
            {user?.mode === "oidc" ? (
              <MenuItem
                onClick={() => {
                  setOpen(false);
                  signOutEverywhere();
                }}
              >
                sign out everywhere
              </MenuItem>
            ) : null}
          </ul>
        </div>
      ) : null}
    </div>
  );
}

function MenuItem({
  children,
  onClick,
}: {
  children: React.ReactNode;
  onClick: () => void;
}) {
  return (
    <li>
      <button
        type="button"
        onClick={onClick}
        className="flex w-full items-center px-3 py-1.5 text-left font-mono text-[12px] text-ink transition-colors hover:bg-surface-2"
      >
        {children}
      </button>
    </li>
  );
}

function userInitials(label: string): string {
  if (!label || label === "—") return "·";
  const parts = label.split(/[@.\s]/).filter(Boolean);
  if (parts.length === 0) return label[0]?.toUpperCase() ?? "·";
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return (parts[0]![0] + parts[1]![0]).toUpperCase();
}

function tierClass(tier: string): string {
  switch (tier) {
    case "admin":
      return "border-red/60 text-red";
    case "maintain":
      return "border-yellow/60 text-yellow";
    case "write":
      return "border-accent/60 text-accent";
    case "triage":
      return "border-green/60 text-green";
    case "read":
      return "border-ink-faint/60 text-ink-muted";
  }
  return "border-ink-faint/50 text-ink-muted";
}
