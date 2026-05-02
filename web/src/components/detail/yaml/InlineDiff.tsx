// InlineDiff — wraps Monaco's DiffEditor in `renderSideBySide: false`
// (vertical/inline) layout. The detail pane is too narrow for side-
// by-side; inline gives the user a top-bottom comparison that fits.
//
// Models are created on mount and disposed on unmount. The component
// rebuilds them when `original` or `proposed` change so the diff
// reflects live edits.

import { useEffect, useRef } from "react";
import * as monaco from "monaco-editor";

import {
  currentMonacoTheme,
  ensureMonacoConfigured,
  useMonacoTheme,
} from "../../../lib/monacoSetup";

interface InlineDiffProps {
  original: string;
  proposed: string;
}

export function InlineDiff({ original, proposed }: InlineDiffProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<monaco.editor.IStandaloneDiffEditor | null>(null);

  useMonacoTheme();

  useEffect(() => {
    if (!containerRef.current) return;
    ensureMonacoConfigured();

    const editor = monaco.editor.createDiffEditor(containerRef.current, {
      theme: currentMonacoTheme(),
      readOnly: true,
      automaticLayout: true,
      renderSideBySide: false,
      fontFamily: '"Geist Mono Variable", ui-monospace, "SF Mono", Menlo, monospace',
      fontSize: 12.5,
      lineHeight: 19,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      enableSplitViewResizing: false,
      renderIndicators: true,
      ignoreTrimWhitespace: false,
      padding: { top: 10, bottom: 10 },
    });
    editorRef.current = editor;

    return () => {
      const model = editor.getModel();
      model?.original.dispose();
      model?.modified.dispose();
      editor.dispose();
      editorRef.current = null;
    };
  }, []);

  // Sync models on every prop change. Disposing the previous models
  // avoids the "model already in use" warning Monaco emits if you
  // setModel() with new instances without disposing the old ones.
  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const prev = editor.getModel();
    const originalModel = monaco.editor.createModel(original, "yaml");
    const modifiedModel = monaco.editor.createModel(proposed, "yaml");
    editor.setModel({ original: originalModel, modified: modifiedModel });
    prev?.original.dispose();
    prev?.modified.dispose();
  }, [original, proposed]);

  return <div ref={containerRef} className="h-full min-h-0 w-full" />;
}
