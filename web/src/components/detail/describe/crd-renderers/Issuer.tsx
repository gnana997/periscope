import { Section, KV } from "../CustomResourceDescribe";
import type { CRDRendererProps } from "./index";

/**
 * cert-manager.io/{Issuer,ClusterIssuer} renderer.
 *
 * Both share the same union spec — exactly one of acme/ca/vault/
 * selfSigned/venafi is set. We pick the present branch and surface
 * the fields that matter for that backend.
 */
const KNOWN_TYPES = ["acme", "ca", "vault", "selfSigned", "venafi"] as const;
type IssuerType = (typeof KNOWN_TYPES)[number];

export function Issuer({ obj }: CRDRendererProps) {
  const spec = (obj.spec ?? {}) as Record<string, unknown>;
  const type = KNOWN_TYPES.find((t) => spec[t] !== undefined);

  return (
    <>
      <Section title="issuer">
        <KV k="type" v={type ? prettyType(type) : "unknown"} />
      </Section>

      {type === "acme" ? <ACMEDetails spec={spec.acme as Record<string, unknown>} /> : null}
      {type === "ca" ? <CADetails spec={spec.ca as Record<string, unknown>} /> : null}
      {type === "vault" ? <VaultDetails spec={spec.vault as Record<string, unknown>} /> : null}
      {type === "selfSigned" ? (
        <Section title="self signed">
          <KV k="—" v="no additional configuration" />
        </Section>
      ) : null}
    </>
  );
}

function prettyType(t: IssuerType): string {
  switch (t) {
    case "acme":
      return "ACME";
    case "ca":
      return "CA";
    case "vault":
      return "Vault";
    case "selfSigned":
      return "Self-Signed";
    case "venafi":
      return "Venafi";
  }
}

function ACMEDetails({ spec }: { spec: Record<string, unknown> }) {
  const pks = (spec.privateKeySecretRef ?? {}) as Record<string, unknown>;
  const solvers = Array.isArray(spec.solvers)
    ? (spec.solvers as Array<Record<string, unknown>>)
    : [];
  return (
    <>
      <Section title="acme">
        {spec.email ? <KV k="email" v={String(spec.email)} /> : null}
        {spec.server ? <KV k="server" v={String(spec.server)} /> : null}
        {pks.name ? <KV k="key secret" v={String(pks.name)} /> : null}
        {spec.skipTLSVerify !== undefined ? (
          <KV k="skip tls verify" v={String(spec.skipTLSVerify)} />
        ) : null}
      </Section>

      {solvers.length > 0 ? (
        <Section title={`solvers (${solvers.length})`}>
          <ul className="space-y-1.5">
            {solvers.map((s, i) => {
              const dns = (s.dns01 ?? {}) as Record<string, unknown>;
              const http = (s.http01 ?? {}) as Record<string, unknown>;
              const dnsKind = Object.keys(dns)[0];
              const httpKind = Object.keys(http)[0];
              const kind = dnsKind
                ? `dns01/${dnsKind}`
                : httpKind
                  ? `http01/${httpKind}`
                  : "unknown";
              return (
                <li
                  key={i}
                  className="rounded-md border border-border bg-surface-2/30 px-2 py-1 text-[11.5px]"
                >
                  <span className="text-ink-muted">{kind}</span>
                </li>
              );
            })}
          </ul>
        </Section>
      ) : null}
    </>
  );
}

function CADetails({ spec }: { spec: Record<string, unknown> }) {
  return (
    <Section title="ca">
      {spec.secretName ? (
        <KV k="secret" v={String(spec.secretName)} />
      ) : null}
      {Array.isArray(spec.crlDistributionPoints) ? (
        <KV
          k="crl"
          v={(spec.crlDistributionPoints as string[]).join(", ")}
        />
      ) : null}
    </Section>
  );
}

function VaultDetails({ spec }: { spec: Record<string, unknown> }) {
  return (
    <Section title="vault">
      {spec.server ? <KV k="server" v={String(spec.server)} /> : null}
      {spec.path ? <KV k="path" v={String(spec.path)} /> : null}
      {spec.namespace ? (
        <KV k="namespace" v={String(spec.namespace)} />
      ) : null}
    </Section>
  );
}
