// Owner proof travels only in the fragment: never sent in a GET or persisted.
export type OwnerSetup = { instanceId: string; code: string; mode?: "local" };
export function captureOwnerSetup(
  href: string,
  replace: (url: string) => void,
): OwnerSetup | null {
  const url = new URL(href);
  const local = url.hash.startsWith("#local=");
  if (!local && !url.hash.startsWith("#setup=")) return null;
  const fields = new URLSearchParams(url.hash.slice(1));
  const field = local ? "local" : "setup";
  const code = fields.get(field) ?? "";
  const instanceId = url.searchParams.get("instance") ?? "";
  const view = fields.get("view") ?? "";
  url.hash = /^[a-zA-Z0-9/_-]{1,128}$/.test(view) ? view : "";
  replace(url.pathname + url.search + url.hash);
  if (
    fields.getAll(field).length !== 1 ||
    fields.has(local ? "setup" : "local") ||
    url.searchParams.getAll("instance").length !== 1 ||
    !/^[A-Za-z0-9_-]{43}$/.test(code) ||
    !/^[A-Za-z0-9_-]{16,128}$/.test(instanceId)
  )
    return null;
  return { instanceId, code, ...(local ? { mode: "local" as const } : {}) };
}
