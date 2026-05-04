// OnboardClusterModal — phase 1 of the agent onboarding UX (#42).
//
// Triggered from the fleet page header (+ Onboard cluster button).
// Walks an admin-tier operator through:
//
//   1. Cluster name input + DNS-1123-ish validation
//   2. POST /api/agents/tokens — server mints a 15-min single-use
//      bootstrap token bound to this cluster name
//   3. Show copy-paste `helm install periscope-agent ...` snippet
//      with the token baked in
//   4. Operator runs the command on the managed cluster
//   5. Refresh the fleet view manually to see the new cluster card
//
// Phase 2 (post-v1.x.0, tracked separately) adds:
//   - Live polling for "agent connected" so the modal can flip to
//     "✓ done" without operator-driven refresh
//   - Per-cluster runtime registry mutations so the operator doesn't
//     have to pre-add the cluster to clusters[] in values.yaml first
//   - Re-mint and decommission flows
//
// What this modal explicitly DOES NOT do (current limitation):
//   - Add the cluster to the registry — operator must have already
//     added a `backend: agent` entry in values.yaml + helm-upgraded
//     the central server. This modal mints a token assuming the name
//     is already registered.

import { useState, type FormEvent } from "react";
import { Modal } from "../ui/Modal";
import { ApiError, mintAgentToken, type AgentTokenIssuance } from "../../lib/api";

interface OnboardClusterModalProps {
  open: boolean;
  onClose: () => void;
  /** Default URL for `--set agent.serverURL=`. Lets the operator
   *  copy a complete command without thinking about their tunnel
   *  hostname. Optional — if not provided, a placeholder shows. */
  agentServerURL?: string;
  /** Default chart version for `--version`. Pulled from /api/features
   *  or the build-time constant; falls back to "1.0.0". */
  chartVersion?: string;
  /** Default install namespace. */
  namespace?: string;
}

// Cluster names: lowercase + digits + dashes, 1-63 chars, no
// leading/trailing/consecutive dashes. Mirrors the server's
// validClusterName check in internal/tunnel/registration.go so the
// UI surfaces the same rule.
const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export function OnboardClusterModal({
  open,
  onClose,
  agentServerURL = "wss://agents.periscope.example.com:8443",
  chartVersion = "1.0.0",
  namespace = "periscope",
}: OnboardClusterModalProps) {
  const [name, setName] = useState("");
  const [pending, setPending] = useState(false);
  const [issuance, setIssuance] = useState<AgentTokenIssuance | null>(null);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setName("");
    setPending(false);
    setIssuance(null);
    setError(null);
  };

  const close = () => {
    reset();
    onClose();
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!NAME_RE.test(name) || name.length > 63) {
      setError(
        "name: lowercase letters, digits, and dashes only; 1-63 chars; no leading/trailing/consecutive dashes",
      );
      return;
    }
    setPending(true);
    try {
      const iss = await mintAgentToken(name);
      setIssuance(iss);
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401) setError("not signed in");
        else if (err.status === 403) setError("admin tier required to mint agent tokens");
        else setError(err.bodyText || `${err.status} ${err.message}`);
      } else {
        setError(String(err));
      }
    } finally {
      setPending(false);
    }
  };

  return (
    <Modal open={open} onClose={close} labelledBy="onboard-cluster-title" size="lg">
      <div className="px-6 py-5">
        <h2
          id="onboard-cluster-title"
          className="font-display text-2xl italic text-ink"
        >
          Onboard a managed cluster
        </h2>
        <p className="mt-2 max-w-xl font-body text-sm text-ink-muted">
          Mint a bootstrap token, run the agent install command on the
          managed cluster, then refresh this page. Full background:{" "}
          <a
            href="https://github.com/gnana997/periscope/blob/main/docs/setup/agent-onboarding.md"
            target="_blank"
            rel="noreferrer"
            className="text-accent underline-offset-2 hover:underline"
          >
            agent onboarding guide
          </a>
          .
        </p>

        {!issuance ? (
          <form onSubmit={submit} className="mt-6 space-y-4">
            <div>
              <label className="block font-mono text-xs uppercase tracking-wider text-ink-muted">
                cluster name
              </label>
              <input
                type="text"
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value.trim())}
                placeholder="prod-eu"
                className="mt-1 w-full rounded-sm border border-border-strong bg-surface px-3 py-2 font-mono text-sm text-ink focus:border-ink focus:outline-none"
                disabled={pending}
              />
              <p className="mt-1 font-mono text-[11px] text-ink-faint">
                Must match a <code>backend: agent</code> entry already
                in your central server's <code>clusters[]</code>. The
                UI doesn't add it for you in v1.x.0.
              </p>
            </div>
            {error && (
              <div className="rounded-sm border border-red bg-red-soft px-3 py-2 font-mono text-xs text-red">
                {error}
              </div>
            )}
            <div className="flex items-center justify-end gap-2 pt-2">
              <button
                type="button"
                onClick={close}
                className="rounded-sm border border-border-strong px-3 py-1.5 font-mono text-xs text-ink-muted hover:border-ink-muted hover:text-ink"
              >
                cancel
              </button>
              <button
                type="submit"
                disabled={pending || !name}
                className="rounded-sm border border-accent bg-accent-soft px-3 py-1.5 font-mono text-xs text-accent hover:bg-accent hover:text-surface disabled:cursor-not-allowed disabled:opacity-50"
              >
                {pending ? "minting…" : "generate install command"}
              </button>
            </div>
          </form>
        ) : (
          <IssuancePanel
            issuance={issuance}
            agentServerURL={agentServerURL}
            chartVersion={chartVersion}
            namespace={namespace}
            onDone={close}
          />
        )}
      </div>
    </Modal>
  );
}

interface IssuancePanelProps {
  issuance: AgentTokenIssuance;
  agentServerURL: string;
  chartVersion: string;
  namespace: string;
  onDone: () => void;
}

function IssuancePanel({
  issuance,
  agentServerURL,
  chartVersion,
  namespace,
  onDone,
}: IssuancePanelProps) {
  const command = buildHelmCommand({
    cluster: issuance.cluster,
    token: issuance.token,
    serverURL: agentServerURL,
    chartVersion,
    namespace,
  });

  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // clipboard blocked — operator can select-all manually
    }
  };

  // Format expiresAt for the operator. The token's TTL is 15 min;
  // show "expires in N min" rather than the absolute timestamp so
  // the urgency is obvious.
  // Captured once at mount via useState initializer to keep render pure
  // (lint: react-hooks/purity rejects bare Date.now() in render).
  const [minutesLeft] = useState(() => Math.max(
    0,
    Math.floor((new Date(issuance.expiresAt).getTime() - Date.now()) / 60000),
  ));

  return (
    <div className="mt-6 space-y-4">
      <div className="rounded-sm border border-border-strong bg-surface-2 px-4 py-3">
        <p className="font-mono text-xs text-ink-muted">
          Token minted for <span className="text-ink">{issuance.cluster}</span>.
          Expires in <span className="text-ink">~{minutesLeft} min</span>.
          Single-use — burn-on-attempt even if the cluster name doesn't match.
        </p>
      </div>

      <div>
        <div className="flex items-center justify-between">
          <label className="font-mono text-xs uppercase tracking-wider text-ink-muted">
            run on the managed cluster
          </label>
          <button
            type="button"
            onClick={copy}
            className="rounded-sm border border-border-strong px-2 py-0.5 font-mono text-[11px] text-ink-muted hover:border-ink-muted hover:text-ink"
          >
            {copied ? "copied ✓" : "copy"}
          </button>
        </div>
        <pre className="mt-1 max-h-72 overflow-auto rounded-sm border border-border-strong bg-surface px-3 py-2 font-mono text-[12px] leading-relaxed text-ink">
          {command}
        </pre>
      </div>

      <div className="rounded-sm border border-amber bg-amber-soft px-3 py-2 font-mono text-[11px] text-amber">
        After the agent connects, refresh the fleet page to see the
        cluster appear. If the token expires before you run the
        install, just close this modal and click + Onboard again.
      </div>

      <div className="flex justify-end pt-2">
        <button
          type="button"
          onClick={onDone}
          className="rounded-sm border border-border-strong px-3 py-1.5 font-mono text-xs text-ink-muted hover:border-ink-muted hover:text-ink"
        >
          done
        </button>
      </div>
    </div>
  );
}

function buildHelmCommand(args: {
  cluster: string;
  token: string;
  serverURL: string;
  chartVersion: string;
  namespace: string;
}): string {
  return [
    "helm upgrade --install periscope-agent \\",
    "  oci://ghcr.io/gnana997/charts/periscope-agent \\",
    `  --version ${args.chartVersion} \\`,
    `  --namespace ${args.namespace} --create-namespace \\`,
    `  --set agent.serverURL=${args.serverURL} \\`,
    `  --set agent.clusterName=${args.cluster} \\`,
    `  --set agent.registrationToken=${args.token}`,
  ].join("\n");
}
