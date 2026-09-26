/** Shared generated-output model used by generators and the render transaction. */

export type GeneratedFile = {
  /** Path relative to generatedDir. */
  relPath: string;
  content: string | Uint8Array;
  mode: number;
  managed: boolean;
};

const MANAGED_MARKER_HASH = "# bento-managed: true\n";
const MANAGED_MARKER_SEMI = "; bento-managed: true\n";

export type ManagedMarkerStyle = "hash" | "semicolon" | "none";

export function withManagedMarker(content: string, style: ManagedMarkerStyle = "hash"): string {
  if (style === "none") return content;
  const marker = style === "semicolon" ? MANAGED_MARKER_SEMI : MANAGED_MARKER_HASH;
  if (
    content.startsWith(MANAGED_MARKER_HASH) ||
    content.startsWith(MANAGED_MARKER_SEMI) ||
    content.startsWith(marker)
  ) {
    return content;
  }
  return `${marker}${content}`;
}

export function isManagedMarker(head: string): boolean {
  return head.startsWith("# bento-managed:") || head.startsWith("; bento-managed:");
}
