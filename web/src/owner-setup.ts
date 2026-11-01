// Owner proof travels only in the fragment: never sent in a GET or persisted.
export type OwnerSetup = { instanceId: string; code: string };
export function captureOwnerSetup(
  href: string,
  replace: (url: string) => void,
): OwnerSetup | null {
  const url = new URL(href);
  if (!url.hash.startsWith("#setup=")) return null;
  const fields = new URLSearchParams(url.hash.slice(1));
  const code = fields.get("setup") ?? "";
  const instanceId = url.searchParams.get("instance") ?? "";
  const view = fields.get("view") ?? "";
  url.hash = /^[a-zA-Z0-9/_-]{1,128}$/.test(view) ? view : "";
  replace(url.pathname + url.search + url.hash);
  if (
    fields.getAll("setup").length !== 1 ||
    url.searchParams.getAll("instance").length !== 1 ||
    !/^[A-Za-z0-9_-]{43}$/.test(code) ||
    !/^[A-Za-z0-9_-]{16,128}$/.test(instanceId)
  )
    return null;
  return { instanceId, code };
}
