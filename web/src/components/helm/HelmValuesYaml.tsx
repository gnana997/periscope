// HelmValuesYaml — Monaco YAML editor for schemaless charts (#74).
//
// Pattern lifted from ApplyYamlInput but stripped: no drag-drop, no
// file picker, no copy button — just an editable YAML pane that
// reflects every keystroke back to the parent via onChange.

import { useEffect, useRef } from "react";
import * as monaco from "monaco-editor";
import {
  MONACO_FONT_FAMILY,
  ensureMonacoConfigured,
  useMonacoTheme,
  currentMonacoTheme,
} from "../../lib/monacoSetup";

interface HelmValuesYamlProps {
  value: string;
  onChange: (next: string) => void;
}

export function HelmValuesYaml({ value, onChange }: HelmValuesYamlProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  // Track whether the editor's current content matches `value` so we
  // don't feedback-loop the cursor when external state updates land
  // (e.g. operator picks a new chart version → values resets to new
  // chart's defaults).
  const lastEmittedRef = useRef<string>(value);

  useMonacoTheme();

  // Mount monaco once.
  useEffect(() => {
    if (!containerRef.current) return;
    ensureMonacoConfigured();
    const editor = monaco.editor.create(containerRef.current, {
      value,
      language: "yaml",
      theme: currentMonacoTheme(),
      readOnly: false,
      automaticLayout: true,
      fontFamily: MONACO_FONT_FAMILY,
      fontSize: 12.5,
      lineHeight: 19,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      renderLineHighlight: "none",
      tabSize: 2,
      wordWrap: "on",
    });
    editorRef.current = editor;
    const sub = editor.onDidChangeModelContent(() => {
      const next = editor.getValue();
      lastEmittedRef.current = next;
      onChange(next);
    });
    return () => {
      sub.dispose();
      editor.dispose();
      editorRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Reflect external `value` changes (parent reset) into the editor.
  useEffect(() => {
    const editor = editorRef.current;
    if (!editor) return;
    if (value === lastEmittedRef.current) return;
    editor.setValue(value);
    lastEmittedRef.current = value;
  }, [value]);

  return <div ref={containerRef} className="h-full w-full" />;
}
