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
  const loopback =
    host === "localhost" ||
    host === "[::1]" ||
    /^127(?:\.\d{1,3}){3}$/.test(host);
  if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback))
    throw new Error(
      "Remote requires HTTPS. Use HTTP only for a loopback SSH tunnel.",
    );
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
    (url.port && (Number(url.port) < 1 || Number(url.port) > 65535)) ||
    /:\/?$/.test(input)
  )
    throw new Error("Invalid instance port.");
  return url.origin;
}
