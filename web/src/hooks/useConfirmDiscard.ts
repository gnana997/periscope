// useConfirmDiscard — guards search-param navigation actions when the
// active YAML editor has unsaved edits.
//
// Pages call this BEFORE updating ?sel / ?selNs / ?tab, because those
// changes unmount the YamlEditor and silently destroy the buffer.
// react-router's useBlocker only fires on pathname changes, and
// beforeunload only fires on hard navigation — neither catches the
// in-page selection / tab switching that drives most of our UX.
//
// Returned wrapper: takes an action thunk; if dirty, prompts the user
// to discard; on accept runs the action, on cancel does nothing. When
// not dirty, runs the action immediately (no prompt churn).
//
// Caller pattern (one line per page):
//
//   const editFlag = useEditorDirty(cluster, "deployments", ns, name);
//   const confirmDiscard = useConfirmDiscard(editFlag.dirty);
//   <DataTable onRowClick={(r) => confirmDiscard(() => setMany({...}))} />
//
// Future polish (Option 2 in the design discussion): persist the
// editor buffer across unmount so navigation never destroys it. Until
// then, this hook is the safety net.

import { useCallback } from "react";

export function useConfirmDiscard(dirty: boolean) {
  return useCallback(
    (action: () => void) => {
      if (dirty) {
        const ok = window.confirm(
          "You have unsaved YAML edits. Discard and continue?",
        );
        if (!ok) return;
      }
      action();
    },
    [dirty],
  );
}
