// ApplyErrorBanner — full-message banner shown above ActionBar when
// an apply attempt fails with a non-409 error. The truncated chip in
// ApplyStatus was unreadable for verbose apiserver / admission-webhook
// errors (multi-line messages, JSON payloads); this surfaces them in
// full with a copy button so operators can paste into incident
// channels.
//
// 409 conflicts are handled by ConflictBanner (separate component);
// this is for anything else: 400 (bad YAML), 403 (RBAC), 422
// (admission webhook denial), 500 (apiserver hiccup), network errors.

import { useState } from "react";
import { cn } from "../../../lib/cn";
import { showToast } from "../../../lib/toastBus";

interface ApplyErrorBannerProps {
  message: string;
  onDismiss: () => void;
}

export function ApplyErrorBanner({ message, onDismiss }: ApplyErrorBannerProps) {
  const [copied, setCopied] = useState(false);
  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(message);
      setCopied(true);
      showToast("error message copied", "success");
      setTimeout(() => setCopied(false), 1500);
    } catch {
      showToast("clipboard unavailable", "warn");
    }
  };

  return (
    <div className="flex shrink-0 items-start gap-3 border-y border-red/50 bg-red-soft px-4 py-2.5">
      <span aria-hidden className="mt-0.5 text-red">
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
          <circle cx="7" cy="7" r="5.5" stroke="currentColor" strokeWidth="1.4" />
          <path d="M7 4v4" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
          <circle cx="7" cy="10" r="0.7" fill="currentColor" />
        </svg>
      </span>
      <div className="min-w-0 flex-1">
        <div className="font-mono text-[11px] font-medium text-red">apply failed</div>
        <pre className="mt-1 max-h-[120px] overflow-auto whitespace-pre-wrap break-words font-mono text-[11.5px] text-ink">
          {message}
        </pre>
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        <button
          type="button"
          onClick={handleCopy}
          className={cn(
            "rounded-sm border px-2 py-1 font-mono text-[10.5px] transition-colors",
            copied
              ? "border-green/50 bg-green-soft text-green"
              : "border-border-strong text-ink-muted hover:border-ink-muted hover:text-ink",
          )}
          title="Copy error message"
        >
          {copied ? "copied" : "copy"}
        </button>
        <button
          type="button"
          onClick={onDismiss}
          className="flex size-6 items-center justify-center rounded-md text-ink-muted hover:bg-surface-2 hover:text-ink"
          aria-label="Dismiss error"
        >
          <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
            <path
              d="M2 2l7 7M9 2l-7 7"
              stroke="currentColor"
              strokeWidth="1.4"
              strokeLinecap="round"
            />
          </svg>
        </button>
      </div>
    </div>
  );
}
