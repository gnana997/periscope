import { useEffect, useState } from "react";
import { useAuth } from "./AuthContext";

/**
 * LoginScreen — pre-auth landing page in OIDC mode.
 *
 * Visual style follows the rest of Periscope: dense, monospace,
 * restrained. One primary action; nothing else competes for attention.
 *
 * The provider label ("Auth0", "Okta", "Microsoft Entra", …) comes
 * from the public /api/auth/config endpoint so the SPA can render the
 * correct copy before any session exists.
 *
 * Two states reachable here:
 *   - default       : "sign in with <provider>" button
 *   - error?msg=... : the user came back from /auth/callback with a
 *                     server-side error (e.g. not_in_allowed_groups);
 *                     show the message inline above the button.
 */
export function LoginScreen() {
  const { signIn, isLoading, error } = useAuth();
  const [providerName, setProviderName] = useState<string>("");

  useEffect(() => {
    let abort = false;
    fetch("/api/auth/config", { headers: { Accept: "application/json" } })
      .then((r) => (r.ok ? r.json() : null))
      .then((j: { providerName?: string } | null) => {
        if (!abort && j?.providerName) setProviderName(j.providerName);
      })
      .catch(() => {
        // Silent: fall through to generic "sign in" copy.
      });
    return () => {
      abort = true;
    };
  }, []);

  const params = new URLSearchParams(window.location.search);
  const calloutMsg = params.get("msg") ?? error;

  const buttonLabel = providerName
    ? `sign in with ${providerName.toLowerCase()}`
    : "sign in";
  const subtle = providerName
    ? `you'll be redirected to your ${providerName.toLowerCase()} tenant.`
    : "you'll be redirected to your identity provider.";

  return (
    <div className="flex h-full items-center justify-center bg-bg">
      <div className="w-full max-w-[360px] px-6">
        <div className="mb-7 text-center">
          <div className="mb-3 inline-flex size-9 items-center justify-center rounded-md bg-accent text-[14px] font-semibold text-white">
            P
          </div>
          <h1 className="font-display text-[20px] tracking-tight text-ink">
            Periscope
          </h1>
          <p className="mt-1 font-mono text-[11.5px] uppercase tracking-[0.08em] text-ink-faint">
            keyless eks dashboard
          </p>
        </div>

        {calloutMsg ? (
          <div className="mb-4 rounded-md border border-red-soft bg-red-soft/30 px-3 py-2 text-[12px] text-red">
            {calloutMsg}
          </div>
        ) : null}

        <button
          type="button"
          disabled={isLoading}
          onClick={signIn}
          className="flex w-full items-center justify-center gap-2 rounded-md border border-border-strong bg-surface px-4 py-2.5 font-mono text-[12.5px] text-ink transition-colors hover:bg-surface-2 focus:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:cursor-not-allowed disabled:opacity-50"
        >
          {isLoading ? (
            <>
              <span
                aria-hidden
                className="block size-3 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent"
              />
              loading…
            </>
          ) : (
            buttonLabel
          )}
        </button>

        <p className="mt-5 text-center font-mono text-[10.5px] text-ink-faint">
          {subtle}
        </p>
      </div>
    </div>
  );
}
