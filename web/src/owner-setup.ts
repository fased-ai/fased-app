// Owner proof travels only in the fragment: never sent in a GET or persisted.
export type OwnerSetup = {
  instanceId: string;
  code: string;
  mode?: "local" | "pair";
};
export function captureOwnerSetup(
  href: string,
  replace: (url: string) => void,
): OwnerSetup | null {
  const url = new URL(href);
  const local = url.hash.startsWith("#local=");
  const pair = url.hash.startsWith("#pair=");
  if (!local && !pair && !url.hash.startsWith("#setup=")) return null;
  const fields = new URLSearchParams(url.hash.slice(1));
  const field = local ? "local" : pair ? "pair" : "setup";
  const code = fields.get(field) ?? "";
  const instanceId = url.searchParams.get("instance") ?? "";
  const view = fields.get("view") ?? "";
  url.hash = /^[a-zA-Z0-9/_-]{1,128}$/.test(view) ? view : "";
  replace(url.pathname + url.search + url.hash);
  const mixed = ["local", "setup", "pair"].some(
    (other) => other !== field && fields.has(other),
  );
  if (
    fields.getAll(field).length !== 1 ||
    mixed ||
    url.searchParams.getAll("instance").length !== 1 ||
    !/^[A-Za-z0-9_-]{43}$/.test(code) ||
    !/^[A-Za-z0-9_-]{16,128}$/.test(instanceId)
  )
    return null;
  const mode = local
    ? ("local" as const)
    : pair
      ? ("pair" as const)
      : undefined;
  return { instanceId, code, ...(mode ? { mode } : {}) };
}
