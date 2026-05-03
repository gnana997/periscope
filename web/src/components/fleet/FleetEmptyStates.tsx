/**
 * FleetEmptyStates — three full-page panels rendered above (or in
 * place of) the cards grid when something page-level needs attention.
 *
 * Visual idiom mirrors components/table/states.tsx EmptyState:
 * Instrument Serif italic display + a short prose paragraph.
 */

interface BaseProps {
  title: string;
  body: React.ReactNode;
}

function Panel({ title, body }: BaseProps) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 py-16 text-center">
      <h3
        className="font-display text-[28px] leading-none tracking-[-0.01em] text-ink-muted"
        style={{ fontWeight: 400, fontStyle: "italic" }}
      >
        {title}
      </h3>
      <div className="max-w-md text-[12.5px] text-ink-muted">{body}</div>
    </div>
  );
}

/** No clusters in the registry — operator hasn't populated clusters.yaml. */
export function FleetEmptyRegistry() {
  return (
    <Panel
      title="no clusters under command yet"
      body={
        <>
          Periscope reads its registry from the{" "}
          <span className="font-mono">clusters.yaml</span> ConfigMap. Add an
          entry and reload — see{" "}
          <a
            href="https://github.com/gnana997/periscope/blob/main/docs/setup/deploy.md"
            className="text-accent underline-offset-2 hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            docs/setup/deploy.md
          </a>
          .
        </>
      }
    />
  );
}

/** Tier-mode user with no group mapping — can't access any cluster. */
export function FleetTierDenied({ tier }: { tier?: string }) {
  return (
    <Panel
      title="no clusters available to your role"
      body={
        <>
          Your tier{tier ? <> (<span className="font-mono">{tier}</span>)</> : ""}{" "}
          does not grant access to any registered cluster. Contact your platform
          team or check{" "}
          <a
            href="https://github.com/gnana997/periscope/blob/main/docs/setup/cluster-rbac.md"
            className="text-accent underline-offset-2 hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            docs/setup/cluster-rbac.md
          </a>
          .
        </>
      }
    />
  );
}

/**
 * "Periscope is up but cannot reach any apiserver" warning rendered
 * ABOVE the cards grid (cards still show their unreachable state below).
 */
export function FleetAllUnreachableBanner() {
  return (
    <div className="flex items-start gap-3 rounded-md border border-red/40 bg-red-soft/60 px-4 py-3 text-[12.5px] text-red">
      <span className="font-mono text-[14px] leading-none">⚠</span>
      <div className="flex flex-col gap-1">
        <strong className="font-medium">
          Periscope is reachable but cannot contact any apiserver.
        </strong>
        <span className="text-ink-muted">
          This usually means the Pod Identity / IRSA association for the
          Periscope pod is missing or misconfigured. Check{" "}
          <a
            href="https://github.com/gnana997/periscope/blob/main/docs/setup/deploy.md"
            className="text-accent underline-offset-2 hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            docs/setup/deploy.md
          </a>
          .
        </span>
      </div>
    </div>
  );
}
