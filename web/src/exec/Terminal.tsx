import { useEffect, useRef } from "react";
import { Terminal as XTerm, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { bindTerminal } from "./term-bus";
import type { ExecClient } from "./ExecClient";

/**
 * Terminal mounts an xterm.js instance and wires it to an ExecClient.
 *
 * The component stays mounted while its tab is hidden — we just toggle
 * `display: none` on the parent so xterm preserves its scrollback and the
 * underlying PTY keeps streaming. xterm internally suppresses paint while
 * its container is display:none, so this is cheap.
 */

interface TerminalProps {
  client: ExecClient;
  /** When true, the terminal becomes the focus target on mount. */
  active: boolean;
}

const FONT_FAMILY =
  '"Geist Mono Variable", ui-monospace, "SF Mono", Menlo, monospace';

/**
 * Read the live CSS custom properties so the xterm theme tracks Periscope's
 * light/dark tokens. Called at mount and whenever the theme toggles.
 */
function readTheme(host: HTMLElement): ITheme {
  const cs = getComputedStyle(host);
  const v = (name: string) => cs.getPropertyValue(name).trim();

  const bg = v("--bg") || "#16140f";
  const ink = v("--ink") || "#ece7d9";
  const inkMuted = v("--ink-muted") || "#9b948a";
  const accent = v("--accent") || "#fb923c";
  const green = v("--green") || "#84cc16";
  const yellow = v("--yellow") || "#fbbf24";
  const red = v("--red") || "#f87171";

  // Pick a slightly cooler complementary blue and magenta so ANSI palettes
  // (used by `ls --color`, `git status`, etc.) stay readable on the warm
  // canvas without clashing with the burnt-orange accent.
  const blue = "#7aa9c4";
  const magenta = "#c084a8";
  const cyan = "#7fb6a8";

  return {
    background: bg,
    foreground: ink,
    cursor: accent,
    cursorAccent: bg,
    selectionBackground: accent + "40",
    selectionForeground: undefined,

    black: "#1f1c16",
    red,
    green,
    yellow,
    blue,
    magenta,
    cyan,
    white: ink,

    brightBlack: inkMuted,
    brightRed: red,
    brightGreen: green,
    brightYellow: yellow,
    brightBlue: blue,
    brightMagenta: magenta,
    brightCyan: cyan,
    brightWhite: ink,
  };
}

export function Terminal({ client, active }: TerminalProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<XTerm | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const unbindRef = useRef<(() => void) | null>(null);

  // Mount xterm once per client.
  useEffect(() => {
    const host = containerRef.current;
    if (!host) return;

    const term = new XTerm({
      fontFamily: FONT_FAMILY,
      fontSize: 12.5,
      lineHeight: 1.25,
      letterSpacing: 0,
      cursorBlink: true,
      cursorStyle: "block",
      cursorWidth: 1,
      scrollback: 5000,
      allowProposedApi: true,
      theme: readTheme(host),
      // Render in DOM mode by default — webgl is faster but webgl-addon
      // adds ~80kb gz and isn't critical for an operator tool with bursty
      // output. The DOM renderer is more robust under tab visibility flips.
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);

    // Initial size — fit may compute (0,0) before layout completes; defer.
    queueMicrotask(() => {
      try {
        fit.fit();
      } catch {
        /* ignore */
      }
    });

    termRef.current = term;
    fitRef.current = fit;
    unbindRef.current = bindTerminal(term, client);

    // Resize the terminal whenever the host changes size. ResizeObserver is
    // the right hammer for the drawer-resize and tab-switch cases.
    const ro = new ResizeObserver(() => {
      if (!fitRef.current) return;
      try {
        fitRef.current.fit();
      } catch {
        /* ignore — happens transiently during tab switches */
      }
    });
    ro.observe(host);

    // Re-read theme tokens when the user toggles between light/dark. The
    // toggle flips a `dark` class on <html>, so we observe that.
    const root = document.documentElement;
    const themeObserver = new MutationObserver(() => {
      const next = readTheme(host);
      term.options.theme = next;
    });
    themeObserver.observe(root, { attributes: true, attributeFilter: ["class"] });

    return () => {
      themeObserver.disconnect();
      ro.disconnect();
      unbindRef.current?.();
      unbindRef.current = null;
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [client]);

  // Re-fit when the tab becomes active. Hidden tabs don't get accurate
  // layout measurements, so they accumulate "stale" sizes; refit on focus.
  useEffect(() => {
    if (!active) return;
    queueMicrotask(() => {
      try {
        fitRef.current?.fit();
        termRef.current?.focus();
      } catch {
        /* ignore */
      }
    });
  }, [active]);

  return (
    <div className="size-full bg-bg p-3">
      <div ref={containerRef} className="size-full" />
    </div>
  );
}
