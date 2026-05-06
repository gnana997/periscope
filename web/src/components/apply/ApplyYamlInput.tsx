// ApplyYamlInput — writable Monaco textarea for the apply dialog with
// a drag-drop overlay accepting .yaml / .yml files plus a file picker
// fallback (Browse… button).
//
// Distinct from src/components/helm/MonacoYAML (read-only) and
// src/components/detail/yaml/YamlEditor (tied to existing-resource
// edit context with URI scoping, dirty state, schema lazy-load). This
// is a simpler writable editor scoped to a single multi-doc paste.

import { useCallback, useEffect, useRef, useState } from "react";
import * as monaco from "monaco-editor";
import { cn } from "../../lib/cn";
import {
  ensureMonacoConfigured,
  useMonacoTheme,
  currentMonacoTheme,
} from "../../lib/monacoSetup";

interface ApplyYamlInputProps {
  value: string;
  onChange: (next: string) => void;
}

export function ApplyYamlInput({ value, onChange }: ApplyYamlInputProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const [dragging, setDragging] = useState(false);

  useMonacoTheme();

  // Mount monaco once. The editor's content is reflected back via
  // onChange; the parent owns the value, but we avoid `editor.setValue`
  // inside the change loop (would feedback-loop the cursor).
  useEffect(() => {
    if (!containerRef.current) return;
    ensureMonacoConfigured();
    const editor = monaco.editor.create(containerRef.current, {
      value,
      language: "yaml",
      theme: currentMonacoTheme(),
      readOnly: false,
      automaticLayout: true,
      fontFamily:
        '"Geist Mono Variable", ui-monospace, "SF Mono", Menlo, monospace',
      fontSize: 12.5,
      lineHeight: 19,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      renderLineHighlight: "none",
      glyphMargin: false,
      folding: true,
      foldingStrategy: "indentation",
      bracketPairColorization: { enabled: false },
      padding: { top: 10, bottom: 10 },
    });
    editorRef.current = editor;

    const sub = editor.onDidChangeModelContent(() => {
      onChange(editor.getValue());
    });

    return () => {
      sub.dispose();
      editor.getModel()?.dispose();
      editor.dispose();
      editorRef.current = null;
    };
    // We intentionally do NOT depend on `value` or `onChange` — the
    // editor's lifecycle is mount-once / dispose-on-unmount; live value
    // sync goes through the imperative `setValue` effect below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Sync external value → editor when it diverges from editor content
  // (e.g. file drop replaces the buffer). Guard against the change-loop
  // by checking before calling setValue.
  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    if (editor.getValue() !== value) {
      editor.setValue(value);
    }
  }, [value]);

  const readFiles = useCallback(
    async (files: FileList | File[]) => {
      const accepted: File[] = [];
      for (const f of Array.from(files)) {
        if (f.name.endsWith(".yaml") || f.name.endsWith(".yml")) {
          accepted.push(f);
        }
      }
      if (accepted.length === 0) return;
      // Concatenate multiple files with `---` separators so a folder
      // drop becomes a multi-doc apply.
      const parts = await Promise.all(accepted.map((f) => f.text()));
      const joined = parts
        .map((p) => p.trimEnd())
        .filter((p) => p.length > 0)
        .join("\n---\n");
      onChange(joined);
    },
    [onChange],
  );

  return (
    <div
      className={cn(
        "relative h-[440px] overflow-hidden rounded-sm border border-border bg-bg",
        dragging && "border-accent ring-2 ring-accent/30",
      )}
      onDragOver={(e) => {
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={(e) => {
        // Only clear when leaving the container, not entering a child.
        if (e.currentTarget.contains(e.relatedTarget as Node)) return;
        setDragging(false);
      }}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        if (e.dataTransfer?.files?.length) {
          void readFiles(e.dataTransfer.files);
        }
      }}
    >
      <div ref={containerRef} className="h-full" />

      {/* File picker fallback — top-right corner */}
      <div className="absolute right-2 top-2 z-10">
        <input
          ref={fileInputRef}
          type="file"
          accept=".yaml,.yml,application/yaml,application/x-yaml,text/yaml"
          multiple
          className="hidden"
          onChange={(e) => {
            if (e.target.files?.length) {
              void readFiles(e.target.files);
              // Clear the input so re-selecting the same file fires onChange
              e.target.value = "";
            }
          }}
        />
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          className="rounded-sm border border-border-strong bg-surface px-2.5 py-1 font-mono text-[11px] lowercase text-ink-muted transition-colors hover:border-accent hover:text-accent"
        >
          browse…
        </button>
      </div>

      {/* Drag-active overlay */}
      {dragging && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center bg-bg/80 backdrop-blur-sm">
          <p className="font-mono text-sm text-accent">
            drop .yaml / .yml file(s)
          </p>
        </div>
      )}

      {/* Empty hint when buffer is empty and not dragging */}
      {!dragging && value.trim().length === 0 && (
        <div className="pointer-events-none absolute inset-x-0 bottom-3 text-center font-mono text-[11px] text-ink-faint">
          paste yaml above, or drag-drop / browse a file
        </div>
      )}
    </div>
  );
}
