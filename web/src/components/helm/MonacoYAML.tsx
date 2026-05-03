// MonacoYAML — read-only Monaco viewer that takes a YAML string.
//
// Lighter than YamlReadView (which fetches via useEditorYaml). The
// Helm pages already have the YAML in TanStack Query cache — they
// just need a renderer.

import { useEffect, useRef, useState } from "react";
import * as monaco from "monaco-editor";
import { cn } from "../../lib/cn";
import {
  ensureMonacoConfigured,
  useMonacoTheme,
  currentMonacoTheme,
} from "../../lib/monacoSetup";

interface MonacoYAMLProps {
  value: string;
  /** Optional placeholder rendered when value is empty (release with
   *  no values overrides, etc.). */
  emptyLabel?: string;
}

export function MonacoYAML({ value, emptyLabel }: MonacoYAMLProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const [copied, setCopied] = useState(false);

  useMonacoTheme();

  useEffect(() => {
    if (!containerRef.current) return;
    ensureMonacoConfigured();
    const editor = monaco.editor.create(containerRef.current, {
      value,
      language: "yaml",
      theme: currentMonacoTheme(),
      readOnly: true,
      automaticLayout: true,
      fontFamily: '"Geist Mono Variable", ui-monospace, "SF Mono", Menlo, monospace',
      fontSize: 12.5,
      lineHeight: 19,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      renderLineHighlight: "none",
      glyphMargin: false,
      folding: true,
      foldingStrategy: "indentation",
      showFoldingControls: "mouseover",
      bracketPairColorization: { enabled: false },
      padding: { top: 10, bottom: 10 },
      stickyScroll: { enabled: true, maxLineCount: 4 },
      contextmenu: false,
      occurrencesHighlight: "off",
      selectionHighlight: false,
    });
    editorRef.current = editor;
    return () => {
      editor.getModel()?.dispose();
      editor.dispose();
      editorRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const model = editor.getModel();
    if (!model) return;
    if (model.getValue() !== value) {
      model.setValue(value);
    }
  }, [value]);

  if (!value && emptyLabel) {
    return (
      <div className="flex h-full items-center justify-center px-6 py-10 text-center font-mono text-[12px] text-ink-faint">
        {emptyLabel}
      </div>
    );
  }

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable — silent */
    }
  };

  return (
    <div className="relative h-full min-h-0">
      <div className="pointer-events-none absolute right-0 top-0 z-10 flex justify-end">
        <button
          type="button"
          onClick={handleCopy}
          className={cn(
            "pointer-events-auto m-2 inline-flex items-center gap-1.5 rounded-md border bg-surface px-2.5 py-1 font-mono text-[11px] shadow-sm transition-colors",
            copied
              ? "border-green/40 bg-green-soft text-green"
              : "border-border text-ink-muted hover:border-border-strong hover:text-ink",
          )}
          aria-label="Copy YAML to clipboard"
        >
          {copied ? "copied" : "copy"}
        </button>
      </div>
      <div ref={containerRef} className="h-full min-h-0" />
    </div>
  );
}
