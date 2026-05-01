import { Link } from "react-router-dom";
import { Section, KV } from "../CustomResourceDescribe";
import type { CRDRendererProps } from "./index";

/**
 * cert-manager.io/Certificate renderer.
 *
 * Surfaces the bits operators actually want: which issuer signs it,
 * which secret it lands in, the DNS names, validity window, and
 * renewal cadence. Falls through whatever exotic spec fields are
 * unknown — the generic conditions/status badges still appear via the
 * frame.
 */
export function Certificate({ obj, cluster, namespace }: CRDRendererProps) {
  const spec = (obj.spec ?? {}) as Record<string, unknown>;
  const status = (obj.status ?? {}) as Record<string, unknown>;
  const issuerRef = (spec.issuerRef ?? {}) as Record<string, unknown>;
  const dnsNames = Array.isArray(spec.dnsNames)
    ? (spec.dnsNames as unknown[]).map(String)
    : [];
  const ipAddresses = Array.isArray(spec.ipAddresses)
    ? (spec.ipAddresses as unknown[]).map(String)
    : [];
  const uris = Array.isArray(spec.uris) ? (spec.uris as unknown[]).map(String) : [];
  const usages = Array.isArray(spec.usages)
    ? (spec.usages as unknown[]).map(String)
    : [];

  const secretLink = spec.secretName && namespace
    ? `/clusters/${cluster}/secrets?ns=${encodeURIComponent(namespace)}&sel=${encodeURIComponent(String(spec.secretName))}&selNs=${encodeURIComponent(namespace)}`
    : null;

  return (
    <>
      <Section title="certificate">
        {spec.commonName ? <KV k="common name" v={String(spec.commonName)} /> : null}
        {issuerRef.name ? (
          <KV
            k="issuer"
            v={`${String(issuerRef.kind ?? "Issuer")}/${String(issuerRef.name)}`}
          />
        ) : null}
        {spec.secretName ? (
          <div className="grid grid-cols-[110px_1fr] gap-3 py-0.5">
            <span className="text-ink-faint">secret</span>
            {secretLink ? (
              <Link
                to={secretLink}
                className="break-all text-accent underline-offset-2 hover:underline"
              >
                {String(spec.secretName)}
              </Link>
            ) : (
              <span className="break-all text-ink">{String(spec.secretName)}</span>
            )}
          </div>
        ) : null}
        {spec.duration ? <KV k="duration" v={String(spec.duration)} /> : null}
        {spec.renewBefore ? (
          <KV k="renew before" v={String(spec.renewBefore)} />
        ) : null}
        {spec.privateKey && typeof spec.privateKey === "object" ? (
          <KV
            k="private key"
            v={Object.entries(spec.privateKey as Record<string, unknown>)
              .map(([k, v]) => `${k}=${String(v)}`)
              .join(", ")}
          />
        ) : null}
      </Section>

      {dnsNames.length > 0 ? (
        <Section title="dns names">
          <ul className="space-y-0.5">
            {dnsNames.map((d) => (
              <li key={d} className="break-all text-[12px] text-ink">
                {d}
              </li>
            ))}
          </ul>
        </Section>
      ) : null}

      {ipAddresses.length > 0 ? (
        <Section title="ip addresses">
          <ul className="space-y-0.5">
            {ipAddresses.map((d) => (
              <li key={d} className="text-[12px] text-ink">
                {d}
              </li>
            ))}
          </ul>
        </Section>
      ) : null}

      {uris.length > 0 ? (
        <Section title="uris">
          <ul className="space-y-0.5">
            {uris.map((d) => (
              <li key={d} className="break-all text-[12px] text-ink">
                {d}
              </li>
            ))}
          </ul>
        </Section>
      ) : null}

      {usages.length > 0 ? (
        <Section title="usages">
          <div className="flex flex-wrap gap-1">
            {usages.map((u) => (
              <span
                key={u}
                className="rounded-md border border-border bg-surface-2/40 px-2 py-0.5 text-[11px] text-ink"
              >
                {u}
              </span>
            ))}
          </div>
        </Section>
      ) : null}

      {(status.notBefore || status.notAfter || status.renewalTime) ? (
        <Section title="validity">
          {status.notBefore ? (
            <KV k="not before" v={String(status.notBefore)} />
          ) : null}
          {status.notAfter ? (
            <KV k="not after" v={String(status.notAfter)} />
          ) : null}
          {status.renewalTime ? (
            <KV k="renewal" v={String(status.renewalTime)} />
          ) : null}
          {status.revision !== undefined ? (
            <KV k="revision" v={String(status.revision)} />
          ) : null}
        </Section>
      ) : null}
    </>
  );
}
