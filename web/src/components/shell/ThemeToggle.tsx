import { useTheme } from "../../hooks/useTheme";

export function ThemeToggle() {
  const [theme, , toggle] = useTheme();
  return (
    <button
      type="button"
      onClick={toggle}
      className="flex size-7 items-center justify-center rounded-md border border-border bg-surface text-ink-muted transition-colors hover:border-border-strong hover:text-ink"
      aria-label={`Switch to ${theme === "light" ? "dark" : "light"} theme`}
      title={`Switch to ${theme === "light" ? "dark" : "light"} theme`}
    >
      {theme === "light" ? <MoonIcon /> : <SunIcon />}
    </button>
  );
}

function MoonIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 13 13" aria-hidden>
      <path
        d="M9.5 8.4a3.9 3.9 0 1 1-4.9-4.9A4.5 4.5 0 1 0 9.5 8.4z"
        fill="currentColor"
      />
    </svg>
  );
}

function SunIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 13 13" aria-hidden>
      <circle cx="6.5" cy="6.5" r="2.4" fill="currentColor" />
      <g stroke="currentColor" strokeWidth="1.2" strokeLinecap="round">
        <path d="M6.5 1.5v1.6M6.5 9.9v1.6M1.5 6.5h1.6M9.9 6.5h1.6M3 3l1.1 1.1M8.9 8.9L10 10M3 10l1.1-1.1M8.9 4.1L10 3" />
      </g>
    </svg>
  );
}
