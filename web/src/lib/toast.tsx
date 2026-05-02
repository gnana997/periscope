// Toaster — viewport for the toast system. Mount once at the app root.
// Toast emission lives in toastBus.ts so react-refresh works smoothly.

import { useEffect, useState } from "react";
import { cn } from "./cn";
import { getToasts, subscribeToasts, type ToastEntry } from "./toastBus";


export function Toaster() {
  const [list, setList] = useState<ToastEntry[]>(() => getToasts());
  useEffect(() => subscribeToasts(setList), []);

  if (list.length === 0) return null;

  return (
    <div
      className="pointer-events-none fixed bottom-6 right-6 z-50 flex flex-col gap-2"
      aria-live="polite"
      aria-atomic="false"
    >
      {list.map((t) => (
        <div
          key={t.id}
          role="status"
          className={cn(
            "pointer-events-auto rounded-md border px-3.5 py-2 font-mono text-[12px] shadow-lg",
            t.tone === "info" && "border-border-strong bg-surface text-ink",
            t.tone === "warn" && "border-yellow/50 bg-yellow-soft text-ink",
            t.tone === "error" && "border-red/50 bg-red-soft text-ink",
            t.tone === "success" && "border-green/50 bg-green-soft text-ink",
          )}
        >
          {t.message}
        </div>
      ))}
    </div>
  );
}
