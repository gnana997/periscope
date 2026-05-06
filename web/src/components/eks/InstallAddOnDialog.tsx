// InstallAddOnDialog — modal for installing an EKS managed add-on
// (issue #119, PR-2).
//
// Single-pane top-to-bottom flow, matching the helm-install dialog
// shape (#74) so operators have one mental model for both:
//
//   1. Open with a CatalogAddon row already selected
//   2. Version dropdown defaults to AWS-default for cluster k8sVer
//   3. On version pick, useAddonConfigurationSchema fires
//   4. Configuration editor: HelmValuesEditor (form when AWS ships a
//      schema; Monaco YAML when it doesn't)
//   5. Optional IAM service-account ARN input
//   6. ResolveConflicts radio (defaults to OVERWRITE — matches the
//      issue mockup)
//   7. Footer: Cancel / Install
//
// On install success: toast, close dialog. The catalog page's
// useAddonCatalog query and the addons-list query are invalidated
// by useInstallAddon (onSettled), so the catalog row flips from
// "+ Install" to "installed" without a page reload, and the
// addons-list status-aware polling picks up the CREATING state.

import { useEffect, useMemo, useState } from "react";
import {
  buildInstallRequest,
  filterCompatibleVersions,
  parseSchemaSafe,
  pickDefaultVersion,
} from "../../lib/addonInstall";
import { ApiError } from "../../lib/api";
import { showToast } from "../../lib/toastBus";
import type { CatalogAddon } from "../../lib/types";
import { Modal } from "../ui/Modal";
import { HelmValuesEditor } from "../helm/HelmValuesEditor";
import {
  useAddonConfigurationSchema,
  useInstallAddon,
} from "../../hooks/useAddonInstall";

interface InstallAddOnDialogProps {
  open: boolean;
  onClose: () => void;
  cluster: string;
  /** Catalog row the operator clicked Install on. Drives the version
   *  dropdown and the marketplace warning. */
  addon: CatalogAddon | null;
  /** Cluster's current K8s version. Filters version dropdown to
   *  compatible entries and picks the AWS-default for the initial
   *  selection. */
  kubernetesVersion?: string;
}

type ResolveConflicts = "NONE" | "OVERWRITE" | "PRESERVE";

export function InstallAddOnDialog({
  open,
  onClose,
  cluster,
  addon,
  kubernetesVersion,
}: InstallAddOnDialogProps) {
  const labelId = "install-addon-dialog-title";

  const [version, setVersion] = useState<string>("");
  const [valuesYaml, setValuesYaml] = useState<string>("");
  const [serviceAccountRoleArn, setServiceAccountRoleArn] = useState<string>("");
  const [resolveConflicts, setResolveConflicts] =
    useState<ResolveConflicts>("OVERWRITE");

  // Compatible-versions list filtered by cluster k8sVer.
  const compatibleVersions = useMemo(
    () => (addon ? filterCompatibleVersions(addon, kubernetesVersion) : []),
    [addon, kubernetesVersion],
  );

  // Reset state every time the dialog opens with a fresh addon —
  // closing + reopening with a different addon must not leak the
  // previous addon's version / values. setState-in-effect is the
  // "external trigger → reset local state" pattern (same precedent
  // as HelmValuesEditor's SchemaFormBridge re-parse on YAML change);
  // the lint rule legitimately allows it but flags anyway.
  useEffect(() => {
    if (!open || !addon) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setVersion(pickDefaultVersion(compatibleVersions));
    setValuesYaml("");
    setServiceAccountRoleArn("");
    setResolveConflicts("OVERWRITE");
  }, [open, addon, compatibleVersions]);

  const schemaQuery = useAddonConfigurationSchema(
    cluster,
    addon?.name ?? "",
    version,
  );
  const parsedSchema = useMemo(
    () => parseSchemaSafe(schemaQuery.data?.configurationSchema),
    [schemaQuery.data?.configurationSchema],
  );

  const installMutation = useInstallAddon(cluster);

  const onInstall = () => {
    if (!addon || !version) return;
    installMutation.mutate(
      buildInstallRequest({
        addonName: addon.name,
        addonVersion: version,
        configurationValuesYaml: valuesYaml,
        serviceAccountRoleArn,
        resolveConflicts,
      }),
      {
        onSuccess: () => {
          showToast(
            `installing ${addon.name} ${version} — status will update as AWS provisions`,
            "success",
          );
          onClose();
        },
        onError: (err) => {
          showToast(`install failed: ${friendlyError(err)}`, "error");
        },
      },
    );
  };

  if (!addon) return null;

  return (
    <Modal
      open={open}
      onClose={onClose}
      labelledBy={labelId}
      size="lg"
      dismissOnBackdrop={!installMutation.isPending}
      dismissOnEsc={!installMutation.isPending}
    >
      <div className="flex max-h-[85vh] flex-col">
        <header className="border-b border-border px-5 py-3">
          <h2 id={labelId} className="text-[15px] font-medium">
            Install <span className="font-mono">{addon.name}</span>
          </h2>
          {(addon.publisher || addon.owner) && (
            <p className="mt-0.5 text-[12px] text-ink-faint">
              {addon.publisher ? `Published by ${addon.publisher}` : null}
              {addon.owner ? ` · owner: ${addon.owner}` : null}
              {addon.type ? ` · ${addon.type}` : null}
            </p>
          )}
          {addon.marketplaceProduct && (
            <p className="mt-1.5 text-[11.5px] text-yellow">
              Marketplace add-on — accept the marketplace EULA in the AWS
              console before installing or AWS will reject the call.
            </p>
          )}
        </header>

        <div className="flex-1 overflow-y-auto px-5 py-4">
          <Section label="Version">
            {compatibleVersions.length === 0 ? (
              <p className="text-[12.5px] text-red">
                No add-on versions are compatible with k8s{" "}
                {kubernetesVersion ?? "(unknown)"} — operator should
                upgrade the cluster or pick a different add-on.
              </p>
            ) : (
              <select
                value={version}
                onChange={(e) => setVersion(e.target.value)}
                className="rounded-sm border border-border bg-bg px-2 py-1 font-mono text-[12.5px]"
              >
                {compatibleVersions.map((v) => (
                  <option key={v.version} value={v.version}>
                    {v.version}
                    {v.default ? "  (recommended)" : ""}
                  </option>
                ))}
              </select>
            )}
          </Section>

          <Section label="Configuration">
            {schemaQuery.isLoading ? (
              <p className="text-[12px] italic text-ink-faint">
                Loading schema…
              </p>
            ) : schemaQuery.isError ? (
              // Schema fetch failed — fall back to YAML editor with
              // a faint banner so the operator knows form mode is
              // unavailable.
              <>
                <p className="mb-2 text-[11.5px] text-yellow">
                  Schema unavailable ({friendlyError(schemaQuery.error)}) —
                  using YAML editor.
                </p>
                <HelmValuesEditor
                  valuesYaml={valuesYaml}
                  onValuesYamlChange={setValuesYaml}
                />
              </>
            ) : (
              <HelmValuesEditor
                valuesYaml={valuesYaml}
                schema={parsedSchema}
                onValuesYamlChange={setValuesYaml}
              />
            )}
          </Section>

          <Section label="IAM service account role (optional)">
            <input
              type="text"
              value={serviceAccountRoleArn}
              onChange={(e) => setServiceAccountRoleArn(e.target.value)}
              placeholder="arn:aws:iam::111111111111:role/addon-role"
              className="w-full rounded-sm border border-border bg-bg px-2 py-1 font-mono text-[11.5px]"
            />
            <p className="mt-1 text-[10.5px] text-ink-faint">
              Leave empty unless the add-on needs IRSA / Pod Identity. If
              set, your AWS role needs <code>iam:PassRole</code> on this
              ARN.
            </p>
          </Section>

          <Section label="Resolve conflicts when fields exist">
            <div className="flex flex-wrap gap-3">
              {(["NONE", "OVERWRITE", "PRESERVE"] as ResolveConflicts[]).map(
                (rc) => (
                  <label
                    key={rc}
                    className="flex items-center gap-1.5 text-[12.5px]"
                  >
                    <input
                      type="radio"
                      name="resolveConflicts"
                      value={rc}
                      checked={resolveConflicts === rc}
                      onChange={() => setResolveConflicts(rc)}
                    />
                    <span className="font-mono text-[11.5px]">{rc}</span>
                  </label>
                ),
              )}
            </div>
            <p className="mt-1 text-[10.5px] text-ink-faint">
              How AWS handles fields the operator already set on the
              cluster. <code>OVERWRITE</code> is the safe default for a
              fresh install; the others matter mainly on upgrade.
            </p>
          </Section>
        </div>

        <footer className="flex items-center justify-end gap-2 border-t border-border bg-surface-2/30 px-5 py-3">
          <button
            type="button"
            onClick={onClose}
            disabled={installMutation.isPending}
            className="rounded-sm border border-border bg-bg px-3 py-1 text-[12.5px] hover:bg-surface-2 disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onInstall}
            disabled={
              installMutation.isPending ||
              !version ||
              compatibleVersions.length === 0
            }
            className="rounded-sm border border-accent bg-accent-soft px-3 py-1 text-[12.5px] text-accent hover:bg-accent/10 disabled:opacity-50"
          >
            {installMutation.isPending ? "Installing…" : "Install"}
          </button>
        </footer>
      </div>
    </Modal>
  );
}

function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <section className="mb-4 last:mb-0">
      <div className="mb-1.5 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        {label}
      </div>
      {children}
    </section>
  );
}

function friendlyError(err: unknown): string {
  if (err instanceof ApiError) {
    return `${err.status} ${err.message}${err.bodyText ? ` — ${err.bodyText.trim()}` : ""}`;
  }
  if (err instanceof Error) return err.message;
  return String(err);
}
