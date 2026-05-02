// toastBus — module-level state for the toast system. Split from the
// Toaster component so react-refresh fast-refresh works correctly
// (single-file react-refresh only allows component exports).

export type ToastTone = "info" | "warn" | "error" | "success";

export interface ToastEntry {
  id: number;
  message: string;
  tone: ToastTone;
}

let nextId = 0;
let toasts: ToastEntry[] = [];
let listeners: Array<(t: ToastEntry[]) => void> = [];

export function showToast(
  message: string,
  tone: ToastTone = "info",
  durationMs = 3000,
): void {
  nextId += 1;
  const entry: ToastEntry = { id: nextId, message, tone };
  toasts = [...toasts, entry];
  notify();
  setTimeout(() => {
    toasts = toasts.filter((t) => t.id !== entry.id);
    notify();
  }, durationMs);
}

export function subscribeToasts(fn: (t: ToastEntry[]) => void): () => void {
  listeners.push(fn);
  return () => {
    listeners = listeners.filter((l) => l !== fn);
  };
}

export function getToasts(): ToastEntry[] {
  return toasts;
}

function notify() {
  for (const l of listeners) l(toasts);
}
