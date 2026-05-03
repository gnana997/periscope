// CanI — render-prop primitive that wraps useCanI + Tooltip so action
// sites swap one line:
//
//   <button disabled={!canEdit}>edit</button>
//
// becomes
//
//   <CanI cluster={c} check={{verb:"patch",resource:"pods",namespace:ns}}>
//     {({ allowed, tooltip, disabledProps }) => (
//       <button {...disabledProps}>edit</button>
//     )}
//   </CanI>
//
// `disabledProps` bundles `disabled` + `aria-disabled` so the caller
// doesn't have to remember both. The tooltip is auto-attached when
// the action is denied; allowed actions render the trigger only.
//
// Why a render prop instead of forcing a Button primitive: the
// codebase deliberately uses raw <button> with inline Tailwind — see
// ResourceActions, OpenShellButton. Render-prop preserves that
// freedom while consolidating the gating.

import type { ReactNode } from "react";
import { useCanI, type CanIDecision } from "../hooks/useCanI";
import type { CanICheck } from "../lib/api";
import { Tooltip } from "./Tooltip";

interface CanIProps {
  cluster: string;
  check: CanICheck;
  children: (state: CanIRenderState) => ReactNode;
}

export interface CanIRenderState extends CanIDecision {
  /**
   * Spread these onto the gated element. `disabled` is a hard HTML
   * disabled (disables clicks, keyboard, form submission) and
   * `aria-disabled` provides the a11y signal even on non-button
   * elements.
   */
  disabledProps: {
    disabled: boolean;
    "aria-disabled": boolean;
  };
}

export function CanI({ cluster, check, children }: CanIProps) {
  const decision = useCanI(cluster, check);
  const disabled = !decision.allowed;
  const node = children({
    ...decision,
    disabledProps: {
      disabled,
      "aria-disabled": disabled,
    },
  });

  // Only attach a tooltip when there's a useful message. Allowed
  // actions render the children unwrapped (no extra DOM noise).
  if (!disabled || !decision.tooltip) {
    return <>{node}</>;
  }

  return <Tooltip content={decision.tooltip}>{node}</Tooltip>;
}
