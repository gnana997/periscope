// EditLabelsModal — structured key/value editor for metadata.labels.
// Replaces "drop into YAML to edit one label" with the primary
// interaction Headlamp / Rancher operators expect. Validation is
// inline (red border + chip) so a typo doesn't make a round trip.
//
// On submit we fire useEditLabels which optimistically writes the new
// labels into the detail cache; MetaPills updates pre-roundtrip.

import { useEffect, useMemo, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import {
  findDuplicateKeys,
  validateLabelKey,
  validateLabelValue,
  type LabelRow,
} from "../../lib/labels";
import { Modal } from "../ui/Modal";

interface EditLabelsModalProps {
  title: string;
  initialLabels: Record<string, string>;
  onClose: () => void;
  onSubmit: (labels: Record<string, string>) => void;
}

let rowIdCounter = 0;

interface KeyedRow extends LabelRow {
  id: number;
}

function fromMap(map: Record<string, string>): KeyedRow[] {
  const entries = Object.entries(map);
  if (entries.length === 0) {
    return [{ id: ++rowIdCounter, key: "", value: "" }];
  }
  return entries.map(([key, value]) => ({
    id: ++rowIdCounter,
    key,
    value,
  }));
}

export function EditLabelsModal({
  title,
  initialLabels,
  onClose,
  onSubmit,
}: EditLabelsModalProps) {
  const [rows, setRows] = useState<KeyedRow[]>(() => fromMap(initialLabels));
  const firstInputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    const t = setTimeout(() => firstInputRef.current?.focus(), 50);
    return () => clearTimeout(t);
  }, []);

  const dups = useMemo(() => findDuplicateKeys(rows), [rows]);

  const errorsByRow = useMemo(() => {
    return rows.map((r) => ({
      key: validateLabelKey(r.key),
      value: validateLabelValue(r.value),
      duplicate: dups.has(r.key) && r.key.length > 0,
    }));
  }, [rows, dups]);

  const hasErrors = errorsByRow.some(
    (e) => e.key !== null || e.value !== null || e.duplicate,
  );
  const initialMap = useMemo(() => normalize(initialLabels), [initialLabels]);
  const currentMap = useMemo(() => {
    const out: Record<string, string> = {};
    for (const r of rows) {
      if (r.key.length > 0) out[r.key] = r.value;
    }
    return normalize(out);
  }, [rows]);
  const unchanged = currentMap === initialMap;

  const submitDisabled = hasErrors || unchanged;

  function update(id: number, patch: Partial<LabelRow>) {
    setRows((prev) =>
      prev.map((r) => (r.id === id ? { ...r, ...patch } : r)),
    );
  }

  function remove(id: number) {
    setRows((prev) => {
      const next = prev.filter((r) => r.id !== id);
      // Always keep at least one row visible so the affordance to add
      // a label is consistent.
      return next.length === 0
        ? [{ id: ++rowIdCounter, key: "", value: "" }]
        : next;
    });
  }

  function add() {
    setRows((prev) => [...prev, { id: ++rowIdCounter, key: "", value: "" }]);
  }

  function submit() {
    if (submitDisabled) return;
    const out: Record<string, string> = {};
    for (const r of rows) {
      if (r.key.length > 0) out[r.key] = r.value;
    }
    onSubmit(out);
    onClose();
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      submit();
    }
  }

  return (
    <Modal open onClose={onClose} labelledBy="edit-labels-title" size="md">
      <div className="border-b border-border px-5 py-3 font-mono text-sm">
        <span className="text-ink-muted">edit labels</span>{" "}
        <span id="edit-labels-title" className="text-ink">
          {title}
        </span>
      </div>

      <div
        className="space-y-2 px-5 py-4"
        onKeyDown={onKeyDown}
      >
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_28px] gap-2 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          <div>key</div>
          <div>value</div>
          <div />
        </div>
        {rows.map((row, i) => {
          const errs = errorsByRow[i];
          const keyMsg = errs.key ?? (errs.duplicate ? "duplicate key" : null);
          const valueMsg = errs.value;
          return (
            <div key={row.id}>
              <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_28px] items-center gap-2">
                <input
                  ref={i === 0 ? firstInputRef : undefined}
                  type="text"
                  value={row.key}
                  onChange={(e) => update(row.id, { key: e.target.value })}
                  placeholder="app.kubernetes.io/name"
                  spellCheck={false}
                  autoComplete="off"
                  className={cn(
                    "min-w-0 rounded-sm border bg-bg px-2 py-1.5 font-mono text-[12.5px] text-ink outline-none",
                    keyMsg ? "border-red" : "border-border",
                    "focus:border-ink-muted",
                  )}
                  aria-invalid={Boolean(keyMsg)}
                />
                <input
                  type="text"
                  value={row.value}
                  onChange={(e) => update(row.id, { value: e.target.value })}
                  placeholder="value"
                  spellCheck={false}
                  autoComplete="off"
                  className={cn(
                    "min-w-0 rounded-sm border bg-bg px-2 py-1.5 font-mono text-[12.5px] text-ink outline-none",
                    valueMsg ? "border-red" : "border-border",
                    "focus:border-ink-muted",
                  )}
                  aria-invalid={Boolean(valueMsg)}
                />
                <button
                  type="button"
                  onClick={() => remove(row.id)}
                  aria-label="remove label"
                  className="rounded-sm border border-border-strong bg-surface px-2 py-1 font-mono text-[12px] text-ink-muted hover:text-ink"
                >
                  ×
                </button>
              </div>
              {(keyMsg || valueMsg) && (
                <div className="mt-1 grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_28px] gap-2 font-mono text-[10.5px] text-red">
                  <div>{keyMsg ?? ""}</div>
                  <div>{valueMsg ?? ""}</div>
                  <div />
                </div>
              )}
            </div>
          );
        })}
        <button
          type="button"
          onClick={add}
          className="mt-1 rounded-sm border border-border-strong bg-surface px-2 py-1 font-mono text-[11.5px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink"
        >
          + add label
        </button>
      </div>

      <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
        <button
          type="button"
          onClick={onClose}
          className="rounded-sm border border-border-strong px-3 py-1.5 font-mono text-sm text-ink-muted transition-colors hover:border-ink-muted hover:text-ink"
        >
          cancel
        </button>
        <button
          type="button"
          onClick={submit}
          disabled={submitDisabled}
          className="rounded-sm bg-accent px-3 py-1.5 font-mono text-sm text-bg transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
          title="⌘/Ctrl+Enter to submit"
        >
          save
        </button>
      </div>
    </Modal>
  );
}

// Sort + serialize a labels map so equality checks (initial vs. current)
// don't false-positive on key-order differences.
function normalize(map: Record<string, string>): string {
  return Object.keys(map)
    .sort()
    .map((k) => `${k}=${map[k]}`)
    .join("\n");
}
