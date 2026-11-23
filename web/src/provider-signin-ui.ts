/**
 * Determine whether a manually pasted remote sign-in callback contains a complete
 * authorization payload ready to submit without extra user clicks.
 */
export function isCompleteSignInCallback(
  provider: string,
  raw: string,
): boolean {
  const trimmed = raw.trim();
  if (!trimmed) return false;
  if (provider === "openrouter") {
    try {
      const url = new URL(trimmed);
      return Boolean(url.searchParams.get("code")?.trim());
    } catch {
      return trimmed.length >= 8 && !/\s/.test(trimmed);
    }
  }
  try {
    const url = new URL(trimmed);
    if (url.protocol !== "http:") return false;
    if (url.hostname !== "127.0.0.1" && url.hostname !== "localhost") {
      return false;
    }
    if (url.pathname !== "/auth/callback") return false;
    return (
      Boolean(url.searchParams.get("code")?.trim()) &&
      Boolean(url.searchParams.get("state")?.trim())
    );
  } catch {
    return false;
  }
}
