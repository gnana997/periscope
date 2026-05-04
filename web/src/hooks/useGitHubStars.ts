import { useEffect, useState } from "react";

const TTL_MS = 6 * 60 * 60 * 1000; // 6h — well clear of the 60/hr unauth rate limit

interface Cached {
  count: number;
  fetchedAt: number;
}

function storageKey(repo: string) {
  return `periscope.gh.stars.${repo}`;
}

function readCached(repo: string): Cached | null {
  try {
    const raw = localStorage.getItem(storageKey(repo));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Cached;
    if (typeof parsed?.count !== "number" || typeof parsed?.fetchedAt !== "number") {
      return null;
    }
    return parsed;
  } catch {
    return null;
  }
}

export function useGitHubStars(repo: string): number | null {
  const [count, setCount] = useState<number | null>(() => {
    const cached = readCached(repo);
    return cached ? cached.count : null;
  });

  useEffect(() => {
    const cached = readCached(repo);
    if (cached && Date.now() - cached.fetchedAt < TTL_MS) return;

    const ctrl = new AbortController();
    fetch(`https://api.github.com/repos/${repo}`, {
      signal: ctrl.signal,
      headers: { Accept: "application/vnd.github+json" },
    })
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
      .then((data: { stargazers_count?: number }) => {
        const n = data.stargazers_count;
        if (typeof n !== "number") return;
        setCount(n);
        localStorage.setItem(
          storageKey(repo),
          JSON.stringify({ count: n, fetchedAt: Date.now() } satisfies Cached),
        );
      })
      .catch(() => {
        // Network/rate-limit failure: keep the previous (possibly stale) value;
        // the link still works, the badge just stays as-is or hidden.
      });

    return () => ctrl.abort();
  }, [repo]);

  return count;
}

export function formatStars(n: number): string {
  if (n < 1000) return String(n);
  const k = n / 1000;
  return k >= 10 ? `${Math.round(k)}k` : `${k.toFixed(1)}k`;
}
