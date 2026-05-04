import type { ComponentType } from "react";
import { cn } from "../../lib/cn";
import { formatStars, useGitHubStars } from "../../hooks/useGitHubStars";

const GITHUB_REPO = "gnana997/periscope";

const LINKS: Array<{ href: string; label: string; Icon: ComponentType }> = [
  { href: "https://periscopehq.vercel.app/docs", label: "Docs", Icon: BookIcon },
  { href: "https://periscopehq.vercel.app/faq", label: "FAQ", Icon: QuestionIcon },
  { href: "https://periscopehq.vercel.app", label: "Homepage", Icon: HomeIcon },
];

// RailLinks — community/help icon column pinned above the user avatar in
// ClusterRail. Out-of-the-way home for OSS-discovery links (docs, faq,
// marketing site) and a GitHub star CTA with a live count badge.
export function RailLinks() {
  return (
    <div className="flex flex-col items-center gap-1.5">
      {LINKS.map(({ href, label, Icon }) => (
        <RailIconLink key={label} href={href} label={label}>
          <Icon />
        </RailIconLink>
      ))}
      <GitHubStarLink />
    </div>
  );
}

function RailIconLink({
  href,
  label,
  children,
}: {
  href: string;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      title={label}
      aria-label={label}
      className={cn(
        "flex size-9 items-center justify-center rounded-lg border border-transparent bg-surface text-ink-muted transition-colors",
        "hover:border-border-strong hover:text-ink",
      )}
    >
      {children}
    </a>
  );
}

function GitHubStarLink() {
  const stars = useGitHubStars(GITHUB_REPO);
  return (
    <a
      href={`https://github.com/${GITHUB_REPO}`}
      target="_blank"
      rel="noopener noreferrer"
      title={stars != null ? `Star on GitHub (${stars})` : "Star on GitHub"}
      aria-label={stars != null ? `Star on GitHub, ${stars} stars` : "Star on GitHub"}
      className={cn(
        "relative flex size-9 items-center justify-center rounded-lg border border-transparent bg-surface text-ink-muted transition-colors",
        "hover:border-border-strong hover:text-ink",
      )}
    >
      <GitHubIcon />
      {stars != null && (
        <span
          className={cn(
            "absolute -bottom-1 -right-1 rounded-sm border border-border bg-surface px-1 py-px",
            "font-mono text-[9px] tabular-nums leading-none text-ink-muted",
          )}
        >
          {formatStars(stars)}
        </span>
      )}
    </a>
  );
}

function BookIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden>
      <path
        d="M2.5 2.5h4a1.5 1.5 0 0 1 1.5 1.5v7a1.2 1.2 0 0 0-1.2-1.2H2.5zM11.5 2.5h-4A1.5 1.5 0 0 0 6 4v7a1.2 1.2 0 0 1 1.2-1.2h4.3z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.1"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function QuestionIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden>
      <circle
        cx="7"
        cy="7"
        r="5.4"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.1"
      />
      <path
        d="M5.4 5.6a1.6 1.6 0 1 1 2.4 1.4c-.5.3-.8.6-.8 1.2v.3"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.1"
        strokeLinecap="round"
      />
      <circle cx="7" cy="10.2" r="0.6" fill="currentColor" />
    </svg>
  );
}

function HomeIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden>
      <path
        d="M2 6.5 7 2.5l5 4V11a1 1 0 0 1-1 1H8.5V8.5h-3V12H3a1 1 0 0 1-1-1z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.1"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function GitHubIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden>
      <path
        fill="currentColor"
        d="M8 .2a8 8 0 0 0-2.5 15.6c.4.07.55-.17.55-.38v-1.34c-2.22.48-2.69-1.07-2.69-1.07-.36-.92-.89-1.17-.89-1.17-.73-.5.06-.49.06-.49.8.06 1.22.83 1.22.83.71 1.23 1.87.87 2.33.67.07-.52.28-.87.5-1.07-1.77-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.01.08-2.12 0 0 .67-.21 2.2.82a7.6 7.6 0 0 1 4 0c1.53-1.03 2.2-.82 2.2-.82.44 1.11.16 1.92.08 2.12.51.56.82 1.28.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48v2.2c0 .21.15.46.55.38A8 8 0 0 0 8 .2z"
      />
    </svg>
  );
}
