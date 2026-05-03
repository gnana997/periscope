import { useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { ReplicaSet, ReplicaSetList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { cn } from "../lib/cn";
import { PageHeader } from "../components/page/PageHeader";
import { SplitPane } from "../components/page/SplitPane";
import { EmptyState, ErrorState, ForbiddenState, LoadingState } from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { DetailPane } from "../components/detail/DetailPane";
import { ReplicaSetDescribe } from "../components/detail/describe/ReplicaSetDescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { useConfirmDiscard } from "../hooks/useConfirmDiscard";
import { ResourceActions } from "../components/edit/ResourceActions";
import { EventsView } from "../components/detail/EventsView";
import { NamespacePicker } from "../components/shell/NamespacePicker";

function isActiveRS(rs: ReplicaSet) {
  return rs.desired > 0;
}

function parseOwnerName(owner: string): string {
  return owner.includes("/") ? owner.split("/")[1] : owner;
}

// Split "deployment-name-a1b2c3d4e" into base + hash
function splitRSName(name: string, ownerName: string): { base: string; hash: string } {
  if (ownerName && name.startsWith(ownerName + "-")) {
    return { base: ownerName, hash: name.slice(ownerName.length + 1) };
  }
  const lastDash = name.lastIndexOf("-");
  if (lastDash > 0) {
    return { base: name.slice(0, lastDash), hash: name.slice(lastDash + 1) };
  }
  return { base: name, hash: "" };
}

interface RSGroup {
  key: string;
  namespace: string;
  deploymentName: string;
  activeRS: ReplicaSet | null;
  dormantRS: ReplicaSet[];
}

// ---- Page ----

export function ReplicaSetsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const namespace = params.get("ns");
  const search = params.get("q") ?? "";
  const selectedNs = params.get("selNs");
  const selectedName = params.get("sel");
  const activeTab = params.get("tab") ?? "describe";

  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());

  const setParam = (key: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value === null || value === "") next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  };

  const setMany = (updates: Record<string, string | null>) => {
    const next = new URLSearchParams(params);
    for (const [key, value] of Object.entries(updates)) {
      if (value === null || value === "") next.delete(key);
      else next.set(key, value);
    }
    setParams(next, { replace: true });
  };

  const query = useResource({ cluster, resource: "replicasets", namespace: namespace ?? undefined });
  const all = useMemo<ReplicaSet[]>(() => (query.data as ReplicaSetList | undefined)?.replicaSets ?? [], [query.data]);

  const filtered = useMemo(
    () => (search ? all.filter((r) => nameMatches(r.name, search)) : all),
    [all, search],
  );

  const { groups, standalone } = useMemo(() => {
    const map = new Map<string, RSGroup>();
    const standalone: ReplicaSet[] = [];

    for (const rs of filtered) {
      if (!rs.owner) {
        standalone.push(rs);
        continue;
      }
      const dep = parseOwnerName(rs.owner);
      const key = `${rs.namespace}/${dep}`;
      if (!map.has(key)) {
        map.set(key, { key, namespace: rs.namespace, deploymentName: dep, activeRS: null, dormantRS: [] });
      }
      const g = map.get(key)!;
      if (isActiveRS(rs)) {
        g.activeRS = rs;
      } else {
        g.dormantRS.push(rs);
      }
    }

    const groups = [...map.values()];
    for (const g of groups) {
      g.dormantRS.sort((a, b) => b.createdAt.localeCompare(a.createdAt));
    }
    groups.sort((a, b) => {
      if (!!a.activeRS !== !!b.activeRS) return a.activeRS ? -1 : 1;
      return a.deploymentName.localeCompare(b.deploymentName);
    });

    return { groups, standalone };
  }, [filtered]);

  const totalActive = all.filter(isActiveRS).length;
  const totalDormant = all.length - totalActive;

  const toggleGroup = (key: string) =>
    setExpandedGroups((prev) => {
      const s = new Set(prev);
      if (s.has(key)) s.delete(key); else s.add(key);
      return s;
    });

  const selectedKey = selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;
  const selectRS = (rs: ReplicaSet) => setMany({ sel: rs.name, selNs: rs.namespace, tab: "describe" });

  const editFlag = useEditorDirty(cluster, "replicasets", selectedNs ?? undefined, selectedName);
  const confirmDiscard = useConfirmDiscard(editFlag.dirty);

  const detail =
    selectedNs && selectedName ? (
      <DetailPane
        title={selectedName}
        subtitle={selectedNs}
        activeTab={activeTab}
        onTabChange={(id) => confirmDiscard(() => setParam("tab", id))}
        onClose={() => confirmDiscard(() => setMany({ sel: null, selNs: null, tab: null }))}
        tabs={[
          { id: "describe", label: "describe", ready: true, content: <ReplicaSetDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} source={{ kind: "builtin", yamlKind: "replicasets" }} ns={selectedNs} name={selectedName} />, dirty: editFlag.dirty },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="replicasets" ns={selectedNs} name={selectedName} /> },
        ]}
        actions={
          <ResourceActions
            cluster={cluster}
            source={{ kind: "builtin", yamlKind: "replicasets" }}
            namespace={selectedNs}
            name={selectedName}
            onDeleted={() => setParam("sel", null)}
          />
        }
      />
    ) : null;

  const groupedList = (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
      {groups.map((group) => (
        <DeploymentGroup
          key={group.key}
          group={group}
          isExpanded={expandedGroups.has(group.key) || !!search}
          onToggle={() => toggleGroup(group.key)}
          selectedKey={selectedKey}
          onSelectRS={selectRS}
          onNavigateToDeployment={() =>
            navigate(
              `/clusters/${cluster}/deployments?sel=${encodeURIComponent(group.deploymentName)}&selNs=${encodeURIComponent(group.namespace)}&tab=describe`,
            )
          }
        />
      ))}
      {standalone.length > 0 && (
        <StandaloneSection rsList={standalone} selectedKey={selectedKey} onSelectRS={selectRS} />
      )}
    </div>
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="ReplicaSets"
        subtitle={
          query.isSuccess
            ? `${groups.length} deployment${groups.length === 1 ? "" : "s"} · ${all.length} revision${all.length === 1 ? "" : "s"}${namespace ? ` in ${namespace}` : ""}`
            : undefined
        }
        streamStatus={query.streamStatus}
        trailing={<NamespacePicker />}
      />

      {/* Filter + stat bar */}
      <div className="flex items-center gap-3 border-b border-border bg-bg px-6 py-2.5">
        <div className="flex min-w-[240px] flex-1 items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-[12.5px] focus-within:border-border-strong">
          <svg width="13" height="13" viewBox="0 0 13 13" className="shrink-0 text-ink-faint" aria-hidden>
            <circle cx="5.5" cy="5.5" r="3.6" stroke="currentColor" strokeWidth="1.3" fill="none" />
            <path d="M8.3 8.3l2.4 2.4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
          </svg>
          <input
            value={search}
            onChange={(e) => setParam("q", e.target.value)}
            placeholder="filter by name"
            className="min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink outline-none placeholder:text-ink-faint"
          />
        </div>
        <div className="flex items-center gap-2 font-mono text-[11px]">
          <span className="flex items-center gap-1.5">
            <span className="block size-1.5 rounded-full bg-green" />
            <span className="tabular-nums text-ink-muted">{totalActive}</span>
            <span className="text-ink-faint">active</span>
          </span>
          <span className="text-ink-faint">·</span>
          <span className="flex items-center gap-1.5">
            <span className="block size-1.5 rounded-full bg-ink-faint/40" />
            <span className="tabular-nums text-ink-muted">{totalDormant}</span>
            <span className="text-ink-faint">dormant</span>
          </span>
        </div>
      </div>

      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? <LoadingState resource="replicasets" /> :
          query.isError ? isForbidden(query.error) ? <ForbiddenState resource="replicasets" /> : isForbidden(query.error) ? <ForbiddenState resource="replicasets" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} /> :
          filtered.length === 0 ? <EmptyState resource="replicasets" namespace={namespace} /> :
          groupedList
        }
        right={detail}
      />
    </div>
  );
}

// ---- DeploymentGroup ----

function DeploymentGroup({
  group,
  isExpanded,
  onToggle,
  selectedKey,
  onSelectRS,
  onNavigateToDeployment,
}: {
  group: RSGroup;
  isExpanded: boolean;
  onToggle: () => void;
  selectedKey: string | null;
  onSelectRS: (rs: ReplicaSet) => void;
  onNavigateToDeployment: () => void;
}) {
  const { deploymentName, namespace, activeRS, dormantRS } = group;
  const hasDormant = dormantRS.length > 0;
  const hasActive = activeRS !== null;

  return (
    <div className="border-b border-border/50 last:border-b-0">
      {/* Group header row */}
      <div className="flex h-8 items-center gap-2 bg-surface-2/30 px-4">
        {/* expand/collapse chevron */}
        {hasDormant ? (
          <button
            onClick={onToggle}
            className="flex size-4 shrink-0 items-center justify-center rounded text-ink-faint transition-colors hover:text-ink-muted"
            aria-label={isExpanded ? "Collapse revisions" : "Expand revisions"}
          >
            <svg
              width="10"
              height="10"
              viewBox="0 0 10 10"
              className={cn("transition-transform duration-150", isExpanded ? "rotate-90" : "rotate-0")}
            >
              <path d="M3 2l4 3-4 3" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" fill="none" />
            </svg>
          </button>
        ) : (
          <div className="size-4 shrink-0" />
        )}

        {/* Status dot */}
        <div
          className={cn(
            "size-1.5 shrink-0 rounded-full",
            hasActive ? "bg-green" : "bg-ink-faint/40",
          )}
        />

        {/* Deployment name — navigates to deployments page */}
        <button
          onClick={onNavigateToDeployment}
          className="font-mono text-[12px] text-ink-muted transition-colors hover:text-ink hover:underline"
          title={`Open Deployment/${deploymentName}`}
        >
          {deploymentName}
        </button>

        <span className="text-[11px] text-ink-faint">{namespace}</span>

        <div className="ml-auto flex items-center gap-3 font-mono text-[10.5px] text-ink-faint">
          {hasDormant && (
            <span>{dormantRS.length} older {dormantRS.length === 1 ? "revision" : "revisions"}</span>
          )}
          {!hasActive && (
            <span className="rounded border border-border px-1 py-px text-[10px] text-ink-faint">no active</span>
          )}
        </div>
      </div>

      {/* Active RS — always visible */}
      {activeRS && (
        <RSRow
          rs={activeRS}
          ownerName={deploymentName}
          isActive
          isSelected={selectedKey === `${activeRS.namespace}/${activeRS.name}`}
          onClick={() => onSelectRS(activeRS)}
        />
      )}

      {/* Dormant RSes — collapsed by default */}
      {hasDormant && (
        <>
          {isExpanded ? (
            dormantRS.map((rs) => (
              <RSRow
                key={rs.name}
                rs={rs}
                ownerName={deploymentName}
                isActive={false}
                isSelected={selectedKey === `${rs.namespace}/${rs.name}`}
                onClick={() => onSelectRS(rs)}
              />
            ))
          ) : (
            <button
              onClick={onToggle}
              className="flex w-full items-center gap-2 px-[52px] py-1.5 text-left text-[11px] text-ink-faint transition-colors hover:bg-surface-2/20 hover:text-ink-muted"
            >
              <span className="font-mono">···</span>
              <span>
                {dormantRS.length} older {dormantRS.length === 1 ? "revision" : "revisions"} — click to expand
              </span>
            </button>
          )}
        </>
      )}
    </div>
  );
}

// ---- RSRow ----

function RSRow({
  rs,
  ownerName,
  isActive,
  isSelected,
  onClick,
}: {
  rs: ReplicaSet;
  ownerName: string;
  isActive: boolean;
  isSelected: boolean;
  onClick: () => void;
}) {
  const { base, hash } = splitRSName(rs.name, ownerName);

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => e.key === "Enter" && onClick()}
      className={cn(
        "flex cursor-pointer items-center gap-3 py-1.5 pl-10 pr-6 text-[12.5px] transition-colors",
        isSelected
          ? "bg-accent/8 border-l-2 border-l-accent pl-[38px]"
          : "hover:bg-surface-2/30",
        !isActive && "opacity-55",
      )}
    >
      {/* Status dot */}
      <div
        className={cn(
          "size-1.5 shrink-0 rounded-full",
          isActive ? "bg-green" : "bg-ink-faint/40",
        )}
      />

      {/* Name: base bold + hash dimmed */}
      <div className="min-w-0 flex-1 font-mono">
        <span className={cn(isActive ? "text-ink" : "text-ink-muted")}>{base}</span>
        {hash && (
          <>
            <span className="text-ink-faint">-</span>
            <span className="text-[11px] text-ink-faint">{hash}</span>
          </>
        )}
      </div>

      {/* Active badge */}
      {isActive && (
        <span className="shrink-0 rounded border border-green/40 bg-green/8 px-1.5 py-px font-mono text-[10px] font-medium text-green">
          active
        </span>
      )}

      {/* Replicas */}
      <div className="w-12 shrink-0 text-right font-mono text-[11.5px] text-ink-muted">
        {isActive ? `${rs.ready}/${rs.desired}` : <span className="text-ink-faint">0/0</span>}
      </div>

      {/* Age */}
      <div className="w-9 shrink-0 text-right font-mono text-[11px] text-ink-faint">
        {ageFrom(rs.createdAt)}
      </div>
    </div>
  );
}

// ---- StandaloneSection (RSes with no Deployment owner) ----

function StandaloneSection({
  rsList,
  selectedKey,
  onSelectRS,
}: {
  rsList: ReplicaSet[];
  selectedKey: string | null;
  onSelectRS: (rs: ReplicaSet) => void;
}) {
  return (
    <div className="border-b border-border/50">
      <div className="flex h-8 items-center gap-2 bg-surface-2/30 px-4">
        <div className="size-4 shrink-0" />
        <div className="size-1.5 shrink-0 rounded-full bg-ink-faint/40" />
        <span className="font-mono text-[12px] text-ink-faint italic">standalone</span>
        <span className="ml-auto font-mono text-[10.5px] text-ink-faint">no owner</span>
      </div>
      {rsList.map((rs) => (
        <RSRow
          key={rs.name}
          rs={rs}
          ownerName=""
          isActive={isActiveRS(rs)}
          isSelected={selectedKey === `${rs.namespace}/${rs.name}`}
          onClick={() => onSelectRS(rs)}
        />
      ))}
    </div>
  );
}
