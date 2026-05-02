// YamlReadView — Monaco-backed read-only YAML viewer.
//
// PR2 deliverable. Replaces the legacy <pre>-grid renderer in
// YamlView.tsx (custom regex highlighter) when ?monaco=1 is in the
// URL. Same useYaml cache key as the legacy view, so toggling between
// the two modes never re-fetches.
//
// Schema-aware features (autocomplete, hover docs, validation) are
// deliberately deferred to PR4. In read mode they don't earn the
// extra ~200KB schema fetch — there's nothing to validate when you
// can't type. PR4 wires monaco-yaml + the OpenAPI proxy together.

import { useEffect, useRef, useState } from "react";
import * as monaco from "monaco-editor";

import { useYaml } from "../../hooks/useResource";
import type { YamlKind } from "../../lib/api";
import { cn } from "../../lib/cn";
import {
  ensureMonacoConfigured,
  useMonacoTheme,
  currentMonacoTheme,
} from "../../lib/monacoSetup";
import { DetailError, DetailLoading } from "./states";

interface YamlReadViewProps {
  cluster: string;
  kind: YamlKind;
  ns: string;
  name: string;
}

export function YamlReadView({ cluster, kind, ns, name }: YamlReadViewProps) {
  const { data, isLoading, isError, error } = useYaml(cluster, kind, ns, name, true);

  if (isLoading) return <DetailLoading label="loading yaml…" />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return <MonacoReadEditor value={data} />;
}

interface MonacoReadEditorProps {
  value: string;
}

function MonacoReadEditor({ value }: MonacoReadEditorProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const [copied, setCopied] = useState(false);

  // Keep Monaco's theme synced with the document's .dark class. Hook
  // is no-op until Monaco is configured, so the order below (configure
  // first, then theme) matters — but useMonacoTheme is safe to call
  // pre-configure since monaco.editor.setTheme tolerates unknown themes.
  useMonacoTheme();

  // Create the editor once. The model lives with the editor; on
  // unmount we dispose both. Re-rendering the parent passes a new
  // `value` prop — we sync it via setValue without disposing.
  useEffect(() => {
    if (!containerRef.current) return;

    ensureMonacoConfigured();

    const editor = monaco.editor.create(containerRef.current, {
      value,
      language: "yaml",
      theme: currentMonacoTheme(),
      readOnly: true,
      automaticLayout: true,
      // Read mode hides the cursor — operators reading YAML shouldn't
      // see a blinking caret as if they could type. Click-and-select
      // for copy still works.
      cursorStyle: "line-thin",
      cursorBlinking: "solid",
      fontFamily: '"Geist Mono Variable", ui-monospace, "SF Mono", Menlo, monospace',
      fontSize: 12.5,
      fontLigatures: true,
      lineHeight: 19,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      smoothScrolling: true,
      renderLineHighlight: "none",
      renderWhitespace: "selection",
      glyphMargin: false,
      folding: true,
      foldingStrategy: "indentation",
      showFoldingControls: "mouseover",
      bracketPairColorization: { enabled: false },
      guides: {
        indentation: true,
        highlightActiveIndentation: false,
        bracketPairs: false,
      },
      scrollbar: {
        vertical: "auto",
        horizontal: "auto",
        verticalScrollbarSize: 10,
        horizontalScrollbarSize: 10,
      },
      padding: { top: 10, bottom: 10 },
      stickyScroll: { enabled: true, maxLineCount: 4 },
      unicodeHighlight: { ambiguousCharacters: false },
      // Disable everything edit-mode that's irrelevant in read.
      contextmenu: false,
      quickSuggestions: false,
      suggestOnTriggerCharacters: false,
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

  // Sync value into the existing model when it changes (e.g. resource
  // refetched after a list invalidation). Avoids re-creating the
  // editor — that would cost a flash and lose scroll position.
  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    const model = editor.getModel();
    if (!model) return;
    if (model.getValue() !== value) {
      // pushEditOperations preserves undo stack vs. setValue, but we're
      // read-only so there's no stack to preserve. setValue is fine and
      // avoids a "your model was modified" decoration flash.
      model.setValue(value);
    }
  }, [value]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable (insecure context, etc.) — silent fail
    }
  };

  return (
    <div className="relative h-full min-h-0">
      {/* Sticky copy chip — same chrome as the legacy view so toggling
          between renderers doesn't shift the user's eye. */}
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
          {copied ? <CheckIcon /> : <ClipboardIcon />}
          {copied ? "copied" : "copy"}
        </button>
      </div>

      <div ref={containerRef} className="h-full min-h-0" />
    </div>
  );
}

function ClipboardIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
      <rect
        x="2.5"
        y="1.5"
        width="6"
        height="8"
        rx="1"
        stroke="currentColor"
        strokeWidth="1.2"
        fill="none"
      />
      <path d="M4 1.5h3v1.5H4z" fill="currentColor" />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
      <path
        d="M2 5.5l2.4 2.4L9 3.2"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
        fill="none"
      />
    </svg>
  );
}
