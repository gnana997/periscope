// Stable per-pod color for multi-pod log attribution.
//
// Hue range 100–340 covers green / cyan / blue / indigo / violet / magenta —
// no overlap with the red and yellow used for log-level coloring, so a
// pod's badge can never be confused with an error/warn highlight.
export function podColor(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
  }
  const hue = 100 + (Math.abs(hash) % 240);
  return `hsl(${hue} 55% 50%)`;
}
