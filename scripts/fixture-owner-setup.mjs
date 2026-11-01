// Disposable browser acceptance only: model the OS-owner launcher's private link.
export async function openOwnerSetup(page, proof) {
  const current = new URL(page.url());
  const health = await (
    await page.request.get(current.origin + "/api/v1/health")
  ).json();
  current.searchParams.set("instance", health.instanceId);
  const view = current.hash.slice(1);
  current.hash = new URLSearchParams({
    setup: proof,
    ...(view ? { view } : {}),
  }).toString();
  await page.goto(current.href);
}
