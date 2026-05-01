import { lazy, Suspense } from "react";
import type { ExecClient } from "./ExecClient";

/**
 * TerminalLazy is the code-split boundary for the xterm.js terminal.
 *
 * Without this wrapper, importing Terminal from Drawer pulls
 * @xterm/xterm + @xterm/addon-fit + xterm.css into the main app chunk —
 * ~150KB minified that the user pays even if they never open a shell.
 * Splitting via React.lazy moves all of it into a separate chunk that
 * loads on demand the first time the drawer renders an active session.
 *
 * Suspense fallback is a tiny mono "loading terminal…" line so users
 * see an intentional progress beat instead of a blank pane during the
 * (typically <300ms) chunk fetch.
 */

const Terminal = lazy(() =>
  import("./Terminal").then((m) => ({ default: m.Terminal })),
);

interface TerminalLazyProps {
  client: ExecClient;
  active: boolean;
}

export function TerminalLazy(props: TerminalLazyProps) {
  return (
    <Suspense fallback={<TerminalLoading />}>
      <Terminal {...props} />
    </Suspense>
  );
}

function TerminalLoading() {
  return (
    <div className="flex size-full items-center justify-center bg-bg font-mono text-[12px] text-ink-faint">
      <span className="flex items-center gap-2">
        <span
          aria-hidden
          className="block size-3 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent"
        />
        loading terminal…
      </span>
    </div>
  );
}
