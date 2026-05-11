// modalRegistry — tracks how many Modal instances are currently open
// so DetailOverlay's keyboard handler can yield Esc when a modal is
// in front of it.
//
// Why a registry instead of capture-phase stopPropagation: Modal.tsx
// listens for Esc at the bubble phase via window.addEventListener
// with no stopPropagation. If DetailOverlay also listens at bubble,
// both fire on the same Esc and the drawer dismisses underneath the
// modal. If DetailOverlay listens at capture and stopPropagations,
// it eats the modal's Esc and the modal never closes.
//
// The counter resolves both: DetailOverlay yields Esc when count > 0,
// so the modal handles Esc on its own. Once the modal unmounts the
// count drops, and the next Esc reaches DetailOverlay.

let openModalCount = 0;

export function notifyModalOpen(): void {
  openModalCount++;
}

export function notifyModalClose(): void {
  openModalCount = Math.max(0, openModalCount - 1);
}

export function isAnyModalOpen(): boolean {
  return openModalCount > 0;
}
