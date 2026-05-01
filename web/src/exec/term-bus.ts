import type { Terminal } from "@xterm/xterm";
import type { ExecClient } from "./ExecClient";

/**
 * Bind an xterm Terminal instance to an ExecClient. Returns a disposer
 * that detaches all subscriptions; call it when unmounting the terminal
 * or replacing its session.
 *
 * The bus does not own either object — it just wires their events.
 */
export function bindTerminal(term: Terminal, client: ExecClient): () => void {
  // stdout from the apiserver → xterm display
  const offStdout = client.onStdout((bytes) => {
    term.write(bytes);
  });

  // keystrokes / paste → stdin (binary frames)
  const onDataDisp = term.onData((data) => {
    client.sendStdin(data);
  });

  // xterm computed a new size (e.g. FitAddon ran) → resize control frame
  const onResizeDisp = term.onResize(({ cols, rows }) => {
    client.sendResize(cols, rows);
  });

  // Send initial resize once the WS is connected so the apiserver shapes
  // the PTY correctly from the first prompt.
  const offHello = client.onHello(() => {
    client.sendResize(term.cols, term.rows);
  });

  return () => {
    offStdout();
    onDataDisp.dispose();
    onResizeDisp.dispose();
    offHello();
  };
}
