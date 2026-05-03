// useEditorDirty — broadcast the editor's dirty bit to the surrounding
// page chrome (specifically the DetailPane tab strip's `yaml*`
// indicator). Implemented via react-query's setQueryData over a
// non-fetching key — the cache becomes a tiny pub/sub channel keyed
// per-resource. Avoids adding a context provider for one boolean.
//
// Producer: YamlEditor calls usePublishEditorDirty() with the current
// dirty state on each transition. Consumer: pages read it via
// useEditorDirty() to populate the Tab `dirty?: boolean` field.

import { skipToken, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { queryKeys } from "../lib/queryKeys";

interface DirtyState {
  dirty: boolean;
}

const DEFAULT: DirtyState = { dirty: false };

function dirtyKey(cluster: string, kind: string, ns: string | undefined, name: string) {
  return queryKeys.edit(cluster, kind, ns ?? "", name);
}

/**
 * Read the dirty bit for a resource. Returns `{ dirty: false }` until
 * a producer publishes a value. Pages call this to wire the tab
 * asterisk; consumers don't trigger network requests (`skipToken`).
 */
export function useEditorDirty(
  cluster: string,
  kind: string,
  ns: string | undefined,
  name: string | null,
): DirtyState {
  const query = useQuery({
    queryKey: dirtyKey(cluster, kind, ns, name ?? ""),
    queryFn: skipToken,
    initialData: DEFAULT,
    enabled: Boolean(name),
  });
  return query.data ?? DEFAULT;
}

/**
 * Producer-side hook: pushes the editor's dirty state into the cache
 * and clears it on unmount. The producer keeps the value live; the
 * cleanup ensures the tab indicator clears when the editor is closed
 * (whether by apply-success, cancel, or unmount).
 */
export function usePublishEditorDirty(
  cluster: string,
  kind: string,
  ns: string | undefined,
  name: string,
  dirty: boolean,
): void {
  const qc = useQueryClient();
  const key = dirtyKey(cluster, kind, ns, name);
  useEffect(() => {
    qc.setQueryData<DirtyState>(key, { dirty });
    return () => {
      qc.setQueryData<DirtyState>(key, DEFAULT);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cluster, kind, ns, name, dirty]);
}
