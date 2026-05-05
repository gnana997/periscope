// ApplyDialogProvider — single-instance ApplyYamlDialog mounted at the
// App root, opened from anywhere via useApplyDialog().
//
// Both entry points (the Sidebar button and the Cmd+K palette's quick
// action) need to be able to open the dialog without owning it
// themselves. Without a shared mount, the SearchPalette's own dialog
// would unmount the moment the palette closes (palette's body returns
// null when !open), losing the dialog mid-flight.
//
// Lives at App level so the dialog persists across page navigations
// while it's open.

import { useCallback, useState, type ReactNode } from "react";
import { ApplyYamlDialog } from "./ApplyYamlDialog";
import { ApplyDialogContext } from "../../contexts/applyDialog";

export function ApplyDialogProvider({ children }: { children: ReactNode }) {
  const [target, setTarget] = useState<string | null>(null);

  const open = useCallback((cluster: string) => setTarget(cluster), []);
  const close = useCallback(() => setTarget(null), []);

  return (
    <ApplyDialogContext.Provider value={{ open }}>
      {children}
      {target !== null && (
        <ApplyYamlDialog open={true} onClose={close} cluster={target} />
      )}
    </ApplyDialogContext.Provider>
  );
}
