/** Lowercase hexadecimal encoding without a runtime-specific dependency. */
export function encodeHex(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString("hex");
}
