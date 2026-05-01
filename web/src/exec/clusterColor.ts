/**
 * Deterministic color stripe for a cluster name. The stripe sits on the
 * left edge of each session tab, giving operators a visual cue for which
 * cluster a session belongs to without forcing them to read the prefix.
 *
 * The hue space is the full 360°, but saturation and lightness are clamped
 * so the stripes blend with Periscope's warm paper / warm dark palette
 * instead of fighting it. Pure RGB primaries are visually loud against the
 * cream/charcoal canvas; muted hues match the operator-tool aesthetic.
 */

// FNV-1a 32-bit; collision rate is fine for the 5–50 clusters a typical
// Periscope deployment will see.
function hashString(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/**
 * Returns an HSL color string with restrained saturation/lightness so the
 * stripe stays calm regardless of theme.
 */
export function clusterStripeColor(cluster: string): string {
  const hue = hashString(cluster) % 360;
  // Avoid exact 60° (yellow) and 30° (orange) which clash with the
  // accent — nudge them toward neutral hues by skipping a 12° window.
  const accentClash =
    (hue >= 18 && hue <= 42) || (hue >= 50 && hue <= 70);
  const finalHue = accentClash ? (hue + 90) % 360 : hue;
  return `hsl(${finalHue}deg 32% 56%)`;
}
