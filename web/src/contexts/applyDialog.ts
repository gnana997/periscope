// applyDialog — React context + hook for the App-level ApplyYamlDialog.
//
// Lives in /contexts (no JSX) so the Provider component can sit in
// /components/apply/ApplyDialogProvider.tsx without tripping the
// react-refresh "single-component-per-file" rule.

import { createContext, useContext } from "react";

export interface ApplyDialogController {
  /** Open the Apply YAML dialog targeting the given cluster. */
  open: (cluster: string) => void;
}

export const ApplyDialogContext = createContext<ApplyDialogController | null>(
  null,
);

export function useApplyDialog(): ApplyDialogController {
  const ctx = useContext(ApplyDialogContext);
  if (!ctx) {
    throw new Error(
      "useApplyDialog called outside of ApplyDialogProvider — wrap App with the provider",
    );
  }
  return ctx;
}
