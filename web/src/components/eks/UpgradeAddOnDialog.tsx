// UpgradeAddOnDialog — modal for upgrading an EKS managed add-on
// (issue #119, PR-3).
//
// Shape parallels InstallAddOnDialog (#119, PR-2):
//
//   1. Open with the operator's chosen installed addon
//   2. Version radio: target version pre-selected to "default" if it's
//      newer than installed; falls back to first newer compatible
//   3. Configuration editor (schema-aware via HelmValuesEditor when
//      AWS ships a schema; YAML otherwise). Pre-populated with the
//      currently-installed configurationValues from the detail blob.
//   4. ResolveConflicts radio (defaults to PRESERVE on upgrade — a
//      different default than install because operators usually want
//      to keep the cluster's current overrides on upgrade)
//   5. Footer: Cancel / Upgrade
//
// On success: toast, close. The catalog query + addons list query
// invalidate (in useUpgradeAddon onSettled) so the row picks up
// status=UPDATING; the status-aware refetchInterval in useAddons /
// useAddon polls the row until ACTIVE / UPDATE_FAILED.

import { useEffect, useMemo, useRef, useState } from "react";
import {
  buildUpgradeRequest,
  filterUpgradeTargets,
  pickUpgradeDefault,
} from "../../lib/addonUpgrade";
import {
  filterCompatibleVersions,
  generateAddonValuesYamlStub,
  parseSchemaSafe,
} from "../../lib/addonInstall";
import { ApiError } from "../../lib/api";
import { showToast } from "../../lib/toastBus";
import type { AddonDetail, CatalogAddon } from "../../lib/types";
import { Modal } from "../ui/Modal";
import { HelmValuesEditor } from "../helm/HelmValuesEditor";
import {
  useAddonConfigurationSchema,
  useUpgradeAddon,
} from "../../hooks/useAddonInstall";

interface UpgradeAddOnDialogProps {
  open: boolean;
  onClose: () => void;
  cluster: string;
  /** The catalog row for this addon — drives the version list +
   *  marketplace warning. May be null while still loading; the
   *  dialog renders a "loading" state. */
  catalogAddon: CatalogAddon | null;
  /** The currently-installed detail blob — drives the config-values
   *  pre-population and "current version" label. */
  detail: AddonDetail | null;
  /** Cluster's current K8s version. Filters version list to
   *  compatible entries. */
  kubernetesVersion?: string;
  /** When set, the dialog opens with this version pre-selected
   *  instead of the AWS-recommended default. Wired by the
   *  detail-pane's clickable version-history rows so an operator
   *  can pick a specific target without a second pass through the
   *  dropdown. Falls back to pickUpgradeDefault when the requested
   *  version isn't actually a valid upgrade target. */
  initialVersion?: string;
}

type ResolveConflicts = "NONE" | "OVERWRITE" | "PRESERVE";

export function UpgradeAddOnDialog({
  open,
  onClose,
  cluster,
  catalogAddon,
  detail,
  kubernetesVersion,
  initialVersion,
}: UpgradeAddOnDialogProps) {
  const labelId = "upgrade-addon-dialog-title";

  const [version, setVersion] = useState<string>("");
  const [valuesYaml, setValuesYaml] = useState<string>("");
  const [serviceAccountRoleArn, setServiceAccountRoleArn] = useState<string>("");
  const [resolveConflicts, setResolveConflicts] =
    useState<ResolveConflicts>("PRESERVE");
  // Editor mode owned by the dialog so version change preserves the
  // operator's explicit Form/YAML toggle. undefined = "let
  // HelmValuesEditor pick auto-default for current schema."
  const [editorMode, setEditorMode] = useState<"form" | "yaml" | undefined>(
    undefined,
  );
  // See InstallAddOnDialog for the seed-vs-edits distinction this
  // ref enables.
  const seededStubRef = useRef<string>("");

  const compatible = useMemo(() => {
    if (!catalogAddon) return [];
    return filterCompatibleVersions(catalogAddon, kubernetesVersion);
  }, [catalogAddon, kubernetesVersion]);

  // Filter out the currently-installed version — upgrading "to the
  // current version" is a noop and AWS rejects it.
  const targets = useMemo(
    () => filterUpgradeTargets(compatible, detail?.version),
    [compatible, detail?.version],
  );

  useEffect(() => {
    if (!open || !catalogAddon || !detail) return;
    // Honor initialVersion if it's actually one of the valid upgrade
    // targets — otherwise fall back to the AWS-recommended default.
    // The pane only proposes versions from the same list, so the
    // fallback path is mostly a guard against stale clicks.
    const requested =
      initialVersion && targets.some((t) => t.version === initialVersion)
        ? initialVersion
        : pickUpgradeDefault(targets);
    /* eslint-disable react-hooks/set-state-in-effect */
    setVersion(requested);
    setValuesYaml(detail.configurationValues ?? "");
    setServiceAccountRoleArn(detail.serviceAccountRoleArn ?? "");
    setResolveConflicts("PRESERVE");
    setEditorMode(undefined);
    seededStubRef.current = "";
    /* eslint-enable react-hooks/set-state-in-effect */
  }, [open, catalogAddon, detail, targets, initialVersion]);

  const schemaQuery = useAddonConfigurationSchema(
    cluster,
    catalogAddon?.name ?? "",
    version,
  );
  const parsedSchema = useMemo(
    () => parseSchemaSafe(schemaQuery.data?.configurationSchema),
    [schemaQuery.data?.configurationSchema],
  );

  // Same seed-on-empty pattern as InstallAddOnDialog: when the
  // operator never set configurationValues for this addon, the
  // editor would open as a blank textarea even with a fully-formed
  // schema. Seed a commented schema reference so YAML mode is
  // discoverable. Skipped when detail.configurationValues is
  // non-empty — that path keeps the operator's prior overrides.
  useEffect(() => {
    if (!open || !parsedSchema) return;
    // Seed and re-seed semantics mirror InstallAddOnDialog. The
    // upgrade dialog seeds detail.configurationValues from AWS up
    // front (in the reset effect above), so the typical operator
    // path lands here with valuesYaml already populated and we
    // don't seed at all. Only when the addon has no stored config
    // (operator never customized) AND the buffer is empty do we
    // seed the discoverability stub.
    const stub = generateAddonValuesYamlStub(parsedSchema);
    const operatorHasEdits =
      valuesYaml !== "" && valuesYaml !== seededStubRef.current;
    if (operatorHasEdits) return;
    if (valuesYaml === stub) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setValuesYaml(stub);
    seededStubRef.current = stub;
  }, [open, parsedSchema, valuesYaml]);

  const upgradeMutation = useUpgradeAddon(cluster);

  const onUpgrade = () => {
    if (!catalogAddon || !version) return;
    upgradeMutation.mutate(
      {
        name: catalogAddon.name,
        request: buildUpgradeRequest({
          addonVersion: version,
          configurationValuesYaml: valuesYaml,
          serviceAccountRoleArn,
          resolveConflicts,
        }),
      },
      {
        onSuccess: () => {
          showToast(
            `upgrading ${catalogAddon.name} to ${version} — status will update as AWS provisions`,
            "success",
          );
          onClose();
        },
        onError: (err) => {
          showToast(`upgrade failed: ${friendlyError(err)}`, "error");
        },
      },
    );
  };

  if (!catalogAddon || !detail) return null;

  return (
    <Modal
      open={open}
      onClose={onClose}
      labelledBy={labelId}
      size="lg"
      dismissOnBackdrop={!upgradeMutation.isPending}
      dismissOnEsc={!upgradeMutation.isPending}
    >
      <div className="flex max-h-[85vh] flex-col">
        <header className="border-b border-border px-5 py-3">
          <h2 id={labelId} className="text-[15px] font-medium">
            Upgrade <span className="font-mono">{catalogAddon.name}</span>
            {detail.version && (
              <span className="ml-2 font-mono text-[11.5px] text-ink-faint">
                · current: {detail.version}
              </span>
            )}
          </h2>
        </header>

        <div className="flex-1 overflow-y-auto px-5 py-4">
          <Section label="Target version">
            {targets.length === 0 ? (
              <p className="text-[12.5px] text-ink-faint">
                No newer versions are compatible with k8s{" "}
                {kubernetesVersion ?? "(unknown)"}. The current version
                is the latest available; upgrade the cluster K8s version
                to unlock newer add-on versions.
              </p>
            ) : (
              <div className="space-y-1.5">
                {targets.map((v) => (
                  <label
                    key={v.version}
                    className="flex items-start gap-2 text-[12.5px]"
                  >
                    <input
                      type="radio"
                      name="targetVersion"
                      value={v.version}
                      checked={version === v.version}
                      onChange={() => setVersion(v.version)}
                      className="mt-0.5"
                    />
                    <span>
                      <span className="font-mono">{v.version}</span>
                      {v.default && (
                        <span className="ml-2 text-[10.5px] text-accent">
                          (recommended)
                        </span>
                      )}
                      <div className="text-[10.5px] text-ink-faint">
                        compatible with k8s{" "}
                        {v.kubernetesVersions.join(", ")}
                      </div>
                    </span>
                  </label>
                ))}
              </div>
            )}
          </Section>

          <Section label="Configuration">
            {schemaQuery.isLoading ? (
              <p className="text-[12px] italic text-ink-faint">
                Loading schema for {version}…
              </p>
            ) : schemaQuery.isError ? (
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
                mode={editorMode}
                onModeChange={setEditorMode}
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
              <code>PRESERVE</code> is the default on upgrade — keeps
              cluster-side overrides intact. <code>OVERWRITE</code>{" "}
              replaces them with the new version's defaults.
            </p>
          </Section>
        </div>

        <footer className="flex items-center justify-end gap-2 border-t border-border bg-surface-2/30 px-5 py-3">
          <button
            type="button"
            onClick={onClose}
            disabled={upgradeMutation.isPending}
            className="rounded-sm border border-border bg-bg px-3 py-1 text-[12.5px] hover:bg-surface-2 disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onUpgrade}
            disabled={
              upgradeMutation.isPending || !version || targets.length === 0
            }
            className="rounded-sm border border-accent bg-accent-soft px-3 py-1 text-[12.5px] text-accent hover:bg-accent/10 disabled:opacity-50"
          >
            {upgradeMutation.isPending ? "Upgrading…" : "Upgrade"}
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
