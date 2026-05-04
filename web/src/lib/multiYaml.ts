// multiYaml — bulk-fetch resource YAML, concat as a multi-document
// stream, and trigger a browser download.
//
// Concurrency is capped at MAX_PARALLEL because browsers limit ~6
// simultaneous connections per origin, and a 100-row fan-out without
// throttling will queue against the live data streams the rest of the
// UI depends on.
//
// Failures are best-effort: a row that 403s or 404s during the fetch
// is collected into `failures` and emitted as a `# FAILED:` comment
// block at the head of the file. The operator gets the snapshot they
// can get — partial results beat all-or-nothing for "GitOps export"
// and "pre-migration dump" use cases.

import { stripForEdit } from "./stripForEdit";

const MAX_PARALLEL = 6;

export interface BulkFetchItem<T> {
  /** Stable ID — used in failure reporting. */
  id: string;
  /** Page-supplied row, opaque to this lib. */
  row: T;
}

export interface BulkFetchFailure {
  id: string;
  reason: string;
}

export interface BulkFetchResult {
  yaml: string;
  successCount: number;
  failures: BulkFetchFailure[];
}

export interface BulkFetchArgs<T> {
  items: BulkFetchItem<T>[];
  /** Per-row YAML fetcher. Receives an AbortSignal for cancellation. */
  fetchYaml: (row: T, signal: AbortSignal) => Promise<string>;
  /** When true, run each fetched YAML through `stripForEdit`. */
  stripServerFields: boolean;
  signal: AbortSignal;
  /** Optional progress callback (`done` / `total`). */
  onProgress?: (done: number, total: number) => void;
}

export async function bulkFetchYaml<T>(
  args: BulkFetchArgs<T>,
): Promise<BulkFetchResult> {
  const { items, fetchYaml, stripServerFields, signal, onProgress } = args;
  const docs: (string | null)[] = new Array(items.length).fill(null);
  const failures: BulkFetchFailure[] = [];
  let done = 0;

  // Simple worker-pool concurrency limiter. Each worker pulls the next
  // index off `cursor` until exhausted. Avoids a Promise.all with N
  // outstanding fetches blowing through the connection pool.
  let cursor = 0;
  const total = items.length;

  const worker = async () => {
    while (true) {
      if (signal.aborted) return;
      const idx = cursor++;
      if (idx >= total) return;
      const it = items[idx];
      try {
        const raw = await fetchYaml(it.row, signal);
        docs[idx] = stripServerFields ? stripForEdit(raw) : raw;
      } catch (err) {
        if (signal.aborted) return;
        failures.push({ id: it.id, reason: errorMessage(err) });
      } finally {
        if (!signal.aborted) {
          done += 1;
          onProgress?.(done, total);
        }
      }
    }
  };

  await Promise.all(
    Array.from({ length: Math.min(MAX_PARALLEL, total) }, worker),
  );

  if (signal.aborted) {
    return { yaml: "", successCount: 0, failures: [] };
  }

  const successDocs = docs.filter((d): d is string => d !== null);
  const header = failures.length > 0 ? buildFailuresHeader(failures) : "";
  // Each /yaml response already ends with a newline from the server's
  // YAML serializer, but be defensive — concat with `---\n` separators
  // and ensure each doc ends in exactly one newline before the marker.
  const body = successDocs.map(ensureTrailingNewline).join("---\n");

  return {
    yaml: header + body,
    successCount: successDocs.length,
    failures,
  };
}

function ensureTrailingNewline(s: string): string {
  if (s.length === 0) return s;
  return s.endsWith("\n") ? s : s + "\n";
}

function buildFailuresHeader(failures: BulkFetchFailure[]): string {
  const lines = failures.map((f) => `#   - ${f.id}: ${f.reason}`);
  return [
    `# Bulk YAML download — ${failures.length} resource(s) failed to fetch:`,
    ...lines,
    "#",
    "",
  ].join("\n");
}

function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

// --- filename ---

/**
 * buildFilename — `<cluster>-<kind>-YYYY-MM-DD-<count>.yaml`, with
 * each segment sanitized so Windows / macOS won't reject the download.
 *
 * K8s context names regularly contain `/` (`gke_proj_zone_name`),
 * `:` (EKS ARNs), and other shell-unfriendly characters. We replace
 * runs of unsafe chars with `_` and trim leading/trailing dashes.
 */
export function buildFilename(
  cluster: string,
  kindLabel: string,
  count: number,
  date: Date = new Date(),
): string {
  const safeCluster = sanitize(cluster);
  const safeKind = sanitize(kindLabel);
  const yyyy = date.getUTCFullYear();
  const mm = String(date.getUTCMonth() + 1).padStart(2, "0");
  const dd = String(date.getUTCDate()).padStart(2, "0");
  return `${safeCluster}-${safeKind}-${yyyy}-${mm}-${dd}-${count}.yaml`;
}

function sanitize(s: string): string {
  return s
    .replace(/[^a-zA-Z0-9._-]+/g, "_")
    .replace(/^[-_.]+|[-_.]+$/g, "")
    .slice(0, 64) || "k8s";
}

// --- download trigger ---

export function triggerYamlDownload(yaml: string, filename: string): void {
  const blob = new Blob([yaml], { type: "application/yaml;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  // Defer revoke so Safari has time to start the download. 0ms tick
  // is enough; no need for setTimeout(_, 1000) overkill.
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
