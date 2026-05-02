// monacoSetup — module-level singleton for Monaco lifecycle + monaco-yaml.
//
// Loaded lazily on first edit/view of a YAML tab. Idempotent — safe to
// call ensureMonacoConfigured() and ensureMonacoYamlConfigured() many
// times. Owns:
//
//   - Worker registration (ESM ?worker imports — see vite.config.ts).
//     Two workers: editor.worker (general) and yaml.worker (monaco-yaml).
//   - Theme registration (periscope-light + periscope-dark) using the
//     same tokens defined in src/index.css.
//   - useMonacoTheme() hook that keeps the active theme synced with the
//     document's `.dark` class via MutationObserver.
//   - monaco-yaml configuration with `enableSchemaRequest: false` (we
//     control schema fetching via lib/api.ts + lib/k8sSchema.ts).
//   - registerSchema() helper for lazy schema registration as resources
//     are opened.

import * as monaco from "monaco-editor";
import EditorWorker from "monaco-editor/esm/vs/editor/editor.worker?worker";
import "monaco-editor/esm/vs/basic-languages/yaml/yaml.contribution.js";
import { configureMonacoYaml, type MonacoYaml, type SchemasSettings } from "monaco-yaml";
import YamlWorker from "monaco-yaml/yaml.worker?worker";
import { useEffect } from "react";

// Set MonacoEnvironment EAGERLY at module-import time, not lazily inside
// ensureMonacoConfigured(). Monaco may create workers as soon as a model
// is created (e.g. via createTextModel), and configureMonacoYaml does
// that synchronously — so we need the dispatcher in place before any
// Monaco call. Setting `self.MonacoEnvironment` at module scope is the
// idiom Monacos own samples use.
(self as unknown as {
  MonacoEnvironment: { getWorker(moduleId: string, label: string): Worker };
}).MonacoEnvironment = {
  getWorker(moduleId: string, label: string): Worker {
    // Diagnostic: log every worker request so misroutes are visible.
    // Remove once monaco-yaml integration is verified stable.
    console.debug("[periscope] Monaco getWorker", { moduleId, label });
    if (label === "yaml" || moduleId.includes("yaml")) return new YamlWorker();
    return new EditorWorker();
  },
};

let configured = false;

export function ensureMonacoConfigured(): void {
  if (configured) return;
  configured = true;
  monaco.editor.defineTheme("periscope-light", periscopeLight);
  monaco.editor.defineTheme("periscope-dark", periscopeDark);
}

/* ============================================================
   monaco-yaml configuration + dynamic schema registration
   ============================================================ */

let monacoYaml: MonacoYaml | null = null;
let registeredSchemas: SchemasSettings[] = [];

/**
 * Idempotent. Initialises monaco-yaml with empty schemas[] and stores
 * the returned MonacoYaml instance for later .update() calls.
 *
 * `enableSchemaRequest: false` means monaco-yaml will NOT fetch
 * schemas from URLs itself — we control fetching via lib/api.ts and
 * push schemas in via registerSchema(). This keeps schema fetching
 * inside react-query (cached, deduped, observable) instead of in a
 * worker we can't see.
 */
export function ensureMonacoYamlConfigured(): MonacoYaml {
  ensureMonacoConfigured();
  if (monacoYaml) return monacoYaml;
  monacoYaml = configureMonacoYaml(monaco, {
    enableSchemaRequest: false,
    validate: true,
    hover: true,
    completion: true,
    format: false, // YAML formatting is opinionated; we leave the user's text as-is
    schemas: [],
  });
  return monacoYaml;
}

/**
 * registerSchema adds a schema to monaco-yaml's active set. Idempotent
 * on `uri` collision — re-registering the same uri replaces (not
 * duplicates). Triggers re-validation of any open models matching the
 * fileMatch pattern.
 */
export function registerSchema(config: { uri: string; fileMatch: string[]; schema?: unknown }): void {
  const my = ensureMonacoYamlConfigured();
  const idx = registeredSchemas.findIndex((s) => s.uri === config.uri);
  if (idx >= 0) {
    registeredSchemas[idx] = config as unknown as SchemasSettings;
  } else {
    registeredSchemas = [...registeredSchemas, config as unknown as SchemasSettings];
  }
  // The .update() call is async (returns a promise) but we don't await
  // here — monaco-yaml re-runs validation in the worker on a debounce.
  // Callers that need post-validation behaviour can call
  // editor.getModelMarkers() after a tick.
  void my.update({ schemas: registeredSchemas });
}

/** Read the current theme from the document — same source of truth as useTheme(). */
export function currentMonacoTheme(): "periscope-light" | "periscope-dark" {
  if (typeof document === "undefined") return "periscope-light";
  return document.documentElement.classList.contains("dark")
    ? "periscope-dark"
    : "periscope-light";
}

/**
 * Keep Monaco's global theme in sync with the document's .dark class.
 *
 * monaco.editor.setTheme() is global — it affects every editor instance,
 * which is what we want (a single dark-mode toggle should flip every
 * open editor). MutationObserver lets us subscribe without importing
 * useTheme directly; any code that mutates the .dark class (today
 * just useTheme) drives Monaco automatically.
 */
export function useMonacoTheme(): void {
  useEffect(() => {
    if (typeof document === "undefined") return;
    monaco.editor.setTheme(currentMonacoTheme());
    const obs = new MutationObserver(() => {
      monaco.editor.setTheme(currentMonacoTheme());
    });
    obs.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    return () => obs.disconnect();
  }, []);
}

/* ============================================================
   Themes — Periscope tokens (warm parchment / warm dark).
   Mirrors the palette in src/index.css and the v2 sketch
   (sketches/yaml-editor-mock.html). Hex literals here are fixed
   per-color-channel; CSS variables aren't usable inside Monaco's
   theme-data objects.
   ============================================================ */

const periscopeLight: monaco.editor.IStandaloneThemeData = {
  base: "vs",
  inherit: true,
  rules: [
    { token: "comment",         foreground: "a39d8f", fontStyle: "italic" },
    { token: "string.yaml",     foreground: "c2410c" },
    { token: "string",          foreground: "c2410c" },
    { token: "number.yaml",     foreground: "3f6212" },
    { token: "number",          foreground: "3f6212" },
    { token: "tag.yaml",        foreground: "6b21a8" },
    { token: "metatag.yaml",    foreground: "6b21a8" },
    { token: "attribute.name",  foreground: "6b21a8" },
    { token: "attribute.value", foreground: "1a1815" },
    { token: "keyword",         foreground: "a16207" },
    { token: "type",            foreground: "1e40af" },
    { token: "delimiter",       foreground: "6b665c" },
  ],
  colors: {
    "editor.background":                  "#f4f1ea",
    "editor.foreground":                  "#1a1815",
    "editorLineNumber.foreground":        "#a39d8f",
    "editorLineNumber.activeForeground":  "#c2410c",
    "editor.selectionBackground":         "#c2410c20",
    "editor.lineHighlightBackground":     "#ebe6db66",
    "editor.lineHighlightBorder":         "#ebe6db00",
    "editorCursor.foreground":            "#c2410c",
    "editorIndentGuide.background":       "#e9e4d8",
    "editorIndentGuide.activeBackground": "#c5bdab",
    "editorGutter.background":            "#f4f1ea",
    "editor.findMatchBackground":         "#c2410c44",
    "editor.findMatchHighlightBackground":"#c2410c22",
    "minimap.background":                 "#f4f1ea",
    "minimapSlider.background":           "#1a181520",
    "scrollbarSlider.background":         "#1a181520",
    "scrollbarSlider.hoverBackground":    "#1a181530",
    "editorOverviewRuler.border":         "#f4f1ea00",
    "editorOverviewRuler.errorForeground":"#b91c1c",
    "editorOverviewRuler.warningForeground":"#a16207",
    "editorWidget.background":            "#fffdf7",
    "editorWidget.border":                "#1a181522",
    "editorBracketMatch.background":      "#c2410c20",
    "editorBracketMatch.border":          "#c2410c80",
    "editorError.foreground":             "#b91c1c",
    "editorWarning.foreground":           "#a16207",
    "diffEditor.insertedTextBackground":  "#3f621222",
    "diffEditor.removedTextBackground":   "#b91c1c22",
    "diffEditor.insertedLineBackground":  "#3f621214",
    "diffEditor.removedLineBackground":   "#b91c1c14",
    "diffEditor.border":                  "#1a181520",
  },
};

const periscopeDark: monaco.editor.IStandaloneThemeData = {
  base: "vs-dark",
  inherit: true,
  rules: [
    { token: "comment",         foreground: "615b50", fontStyle: "italic" },
    { token: "string.yaml",     foreground: "fb923c" },
    { token: "string",          foreground: "fb923c" },
    { token: "number.yaml",     foreground: "84cc16" },
    { token: "number",          foreground: "84cc16" },
    { token: "tag.yaml",        foreground: "a78bfa" },
    { token: "metatag.yaml",    foreground: "a78bfa" },
    { token: "attribute.name",  foreground: "a78bfa" },
    { token: "attribute.value", foreground: "ece7d9" },
    { token: "keyword",         foreground: "fbbf24" },
    { token: "type",            foreground: "60a5fa" },
    { token: "delimiter",       foreground: "9b948a" },
  ],
  colors: {
    "editor.background":                  "#16140f",
    "editor.foreground":                  "#ece7d9",
    "editorLineNumber.foreground":        "#615b50",
    "editorLineNumber.activeForeground":  "#fb923c",
    "editor.selectionBackground":         "#fb923c33",
    "editor.lineHighlightBackground":     "#1f1c1666",
    "editor.lineHighlightBorder":         "#1f1c1600",
    "editorCursor.foreground":            "#fb923c",
    "editorIndentGuide.background":       "#1f1c16",
    "editorIndentGuide.activeBackground": "#3a342a",
    "editorGutter.background":            "#16140f",
    "editor.findMatchBackground":         "#fb923c66",
    "editor.findMatchHighlightBackground":"#fb923c33",
    "minimap.background":                 "#16140f",
    "minimapSlider.background":           "#ffffff14",
    "scrollbarSlider.background":         "#ffffff14",
    "scrollbarSlider.hoverBackground":    "#ffffff24",
    "editorOverviewRuler.border":         "#16140f00",
    "editorOverviewRuler.errorForeground":"#f87171",
    "editorOverviewRuler.warningForeground":"#fbbf24",
    "editorWidget.background":            "#1f1c16",
    "editorWidget.border":                "#ffffff14",
    "editorBracketMatch.background":      "#fb923c22",
    "editorBracketMatch.border":          "#fb923c80",
    "editorError.foreground":             "#f87171",
    "editorWarning.foreground":           "#fbbf24",
    "diffEditor.insertedTextBackground":  "#84cc1622",
    "diffEditor.removedTextBackground":   "#f8717122",
    "diffEditor.insertedLineBackground":  "#84cc1614",
    "diffEditor.removedLineBackground":   "#f8717114",
    "diffEditor.border":                  "#ffffff14",
  },
};
