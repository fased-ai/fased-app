import { chromium } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
const root = resolve(".evidence/f2-terminal"),
  out = resolve(root, "comparisons");
await mkdir(out, { recursive: true });
const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.FASED_TEST_BROWSER,
});
for (const [name, size, surface, width, height] of [
  ["desktop-apps", "desktop", "app", 1440, 900],
  ["mobile-apps", "mobile", "app", 390, 1000],
  ["desktop-landings", "desktop", "landing", 1440, 1900],
  ["mobile-landings", "mobile", "landing", 390, 0],
]) {
  const images = ["wen", "fased"]
    .map(
      (host) =>
        `<section><h2>${host === "wen" ? "WEN" : "Fased"}</h2><div style="${height ? `height:${height}px;overflow:hidden;` : ""}"><img src="${pathToFileURL(resolve(root, "screenshots", `${host}-${size}-${surface}.png`))}"/></div></section>`,
    )
    .join("");
  const allocations =
    name === "mobile-apps"
      ? ["wen", "fased"]
          .map(
            (host) =>
              `<section><h2>${host === "wen" ? "WEN" : "Fased"} · Allocate</h2><img src="${pathToFileURL(resolve(root, "screenshots", `${host}-mobile-allocate.png`))}"/></section>`,
          )
          .join("")
      : "";
  const html = `<!doctype html><meta charset="utf-8"><title>${name}</title><style>*{box-sizing:border-box}body{margin:0;padding:16px;background:#15141c;color:#f3f2f7;font:18px system-ui}main{display:grid;grid-template-columns:repeat(2,${width}px);gap:16px}h2{font-size:24px;margin:4px 0 12px}img{display:block;width:100%;border:1px solid #373447}p{color:#b7b2c8}</style><p>F2 local fixture builds · no live execution${height ? " · viewport comparison" : ""}</p><main>${images}${allocations}</main>`;
  const file = resolve(out, `${name}.html`);
  await writeFile(file, html);
  const page = await browser.newPage({
    viewport: { width: width * 2 + 48, height: 900 },
    deviceScaleFactor: 1,
  });
  await page.goto(pathToFileURL(file).href);
  await page
    .locator("img")
    .evaluateAll((imgs) => Promise.all(imgs.map((i) => i.decode())));
  await page.screenshot({ path: resolve(out, `${name}.png`), fullPage: true });
  await page.close();
}
await browser.close();
console.log(out);
