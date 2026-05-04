// Brand — header wordmark + dynamic version eyebrow.
//
// Pulls the server's binary version from /api/features (already
// fetched once at app boot via useFeatures + cached for the
// session). Falls back to a non-claim ("…") while the features
// query is in flight; the placeholder is short-lived (single round-
// trip on first paint) and a brief skeleton is preferable to a
// stale literal.
//
// Channel suffix logic:
//   - "stable" releases (e.g. v1.0.0) render as just "v1.0.0"
//   - prereleases (anything with a `-`, e.g. v1.0.0-rc4) render
//     with " · prerelease" so operators visually flag they're not
//     on a stable
//   - "dev" builds (no ldflags) render with " · dev"
//
// If you change this format, also check periscopehq.dev's
// marketing-site Latest Release widget — the two strings
// historically rhymed and divergence reads as a bug.

import { useFeatures } from "../../lib/features";

export function Brand() {
  const { data } = useFeatures();

  return (
    <div className="px-5 pt-5 pb-3">
      <div className="flex items-baseline gap-2">
        <h1
          className="font-display text-[22px] leading-none tracking-tight text-ink"
          style={{ fontWeight: 400 }}
        >
          Periscope
        </h1>
        <span className="text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          {versionEyebrow(data?.version, data?.channel)}
        </span>
      </div>
    </div>
  );
}

function versionEyebrow(
  version: string | undefined,
  channel: "stable" | "prerelease" | "dev" | undefined,
): string {
  if (!version) {
    // Pre-features-resolve: render a skeleton glyph rather than a
    // version string so the operator doesn't briefly see a stale
    // literal between paint and fetch.
    return "…";
  }
  if (channel === "stable") return version;
  if (channel === "prerelease") return `${version} · prerelease`;
  if (channel === "dev") return `${version} · dev`;
  return version;
}
