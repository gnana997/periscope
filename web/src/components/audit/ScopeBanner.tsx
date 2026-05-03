/**
 * ScopeBanner renders above the FilterStrip when the user's audit
 * scope is "self" — i.e. they can only see their own actions.
 *
 * Editorial voice consistent with Fleet's empty states. NOT
 * dismissable — it's a constant context cue, not a notification.
 */
export function ScopeBanner() {
  return (
    <aside
      role="note"
      aria-label="Audit scope notice"
      className="flex items-start gap-4 rounded-md border border-border bg-surface px-5 py-4"
    >
      <span
        aria-hidden
        className="font-display text-[28px] leading-[0.85] text-ink-muted"
        style={{ fontWeight: 400, fontStyle: "italic" }}
      >
        ¶
      </span>
      <div className="flex flex-col gap-1.5">
        <h3
          className="font-display text-[20px] leading-tight tracking-[-0.01em] text-ink"
          style={{ fontWeight: 400, fontStyle: "italic" }}
        >
          You see what you did, not what colleagues did.
        </h3>
        <p className="text-[12.5px] text-ink-muted">
          This is the self-only audit view. Talk to your platform team if you
          need broader visibility — they can grant audit-admin access via{" "}
          <code className="rounded bg-surface-2 px-1 py-0.5 font-mono text-[11px]">
            auth.authorization.auditAdminGroups
          </code>
          . Reference:{" "}
          <a
            href="https://github.com/gnana997/periscope/blob/main/docs/setup/audit.md"
            className="text-accent underline-offset-2 hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            docs/setup/audit.md
          </a>
          .
        </p>
      </div>
    </aside>
  );
}
