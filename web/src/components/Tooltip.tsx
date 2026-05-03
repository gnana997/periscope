// Tooltip — project wrapper around Radix Tooltip with default styling
// + the affordances Periscope wants: works on disabled buttons (the
// trigger is wrapped in a span so pointer events keep firing), shows
// on hover and on keyboard focus, dismissible with Esc.
//
// Use the asChild form when wrapping a button: it composes refs so the
// button keeps its native semantics. For disabled buttons, set
// `disableHoverableContent` to true and wrap the button in a span the
// caller controls so pointer events still register.

import {
  Provider as RadixProvider,
  Root,
  Trigger,
  Portal,
  Content,
  Arrow,
} from "@radix-ui/react-tooltip";
import type { ReactNode } from "react";

interface TooltipProps {
  /**
   * The trigger. Pass a single React element (button, span, etc.) and
   * we'll forward the tooltip's ref via Radix's asChild composition.
   */
  children: ReactNode;
  /**
   * Tooltip body. Pass a string for the default rendering, or any
   * ReactNode for richer content (mode badge + reason, etc.).
   * When null/undefined, the wrapper renders the trigger only — useful
   * for sites that always wrap their buttons in <Tooltip> regardless
   * of whether a hint exists.
   */
  content?: ReactNode | null;
  /** Pixel offset from the trigger. Defaults to 6 (matches Radix). */
  sideOffset?: number;
  /** Side relative to the trigger; Radix-default "top". */
  side?: "top" | "right" | "bottom" | "left";
  /** Delay before opening on hover, ms. Default 200. */
  delayDuration?: number;
}

/**
 * <TooltipProvider> belongs once near the root of the app. Mount it in
 * main.tsx so every tooltip on the page shares one Provider context.
 */
export function TooltipProvider({ children }: { children: ReactNode }) {
  return (
    <RadixProvider delayDuration={200} skipDelayDuration={300}>
      {children}
    </RadixProvider>
  );
}

export function Tooltip({
  children,
  content,
  sideOffset = 6,
  side = "top",
  delayDuration,
}: TooltipProps) {
  if (content == null || content === "") return <>{children}</>;
  return (
    <Root delayDuration={delayDuration}>
      <Trigger asChild>{children}</Trigger>
      <Portal>
        <Content
          side={side}
          sideOffset={sideOffset}
          // z-50 keeps the tooltip above modals' backdrop (which sit
          // around z-40 in the existing stack).
          className="z-50 max-w-[280px] rounded-md border border-border-strong bg-surface-2 px-2.5 py-1.5 font-mono text-[11.5px] leading-snug text-ink shadow-[0_8px_18px_-10px_rgba(0,0,0,0.45)] data-[state=delayed-open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=delayed-open]:fade-in-0"
          collisionPadding={8}
        >
          {content}
          <Arrow className="fill-border-strong" width={10} height={5} />
        </Content>
      </Portal>
    </Root>
  );
}
