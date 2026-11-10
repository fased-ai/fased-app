export function instanceOrigin(value: string): string {
  const input = value.trim();
  if (
    input.length > 2048 ||
    !/^https?:\/\/[^/?#]+\/?$/.test(input) ||
    !/^https?:\/\//.test(input) ||
    /[\\\u0000-\u001f\u007f]/.test(input)
  )
    throw new Error("Enter an HTTPS instance address.");
  let url: URL;
  try {
    url = new URL(input);
  } catch {
    throw new Error("Enter an HTTPS instance address.");
  }
  const host = url.hostname.toLowerCase();
  const explicitPort = input.match(/:(\d+)\/?$/)?.[1] ?? "";
  if (
    url.protocol !== "https:" ||
    !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*\.ts\.net$/.test(
      host,
    ) ||
    (explicitPort !== "" && explicitPort !== url.port) ||
    (url.port !== "" && Number(url.port) < 1024)
  )
    throw new Error("Remote requires its Tailscale HTTPS address.");
  if (
    url.username ||
    url.password ||
    url.pathname !== "/" ||
    input.includes("?") ||
    input.includes("#")
  )
    throw new Error(
      "Use an instance origin without credentials, path, query or fragment.",
    );
  if (
    (url.port && (Number(url.port) < 1024 || Number(url.port) > 65535)) ||
    /:\/?$/.test(input)
  )
    throw new Error("Invalid instance port.");
  return url.origin;
}
