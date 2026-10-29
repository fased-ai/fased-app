import assert from "node:assert/strict";
import test from "node:test";
import { validateFoundation, validateDocumentation } from "./check-docs.mjs";

function foundation(extra = {}) {
  return new Map(
    Object.entries({
      ".gitignore": ".evidence/\n",
      "AGENTS.md": "# Contributor notes\n",
      "CONTRIBUTING.md": "# Contributing\n\nRead [the notes](AGENTS.md).\n",
      LICENSE: "License fixture\n",
      NOTICE: "Notice fixture\n",
      "docs/README.md":
        "# Product guide\n\nRead [mining](mining.md#round-results) and [Fased](https://docs.fased.ai).\n",
      "docs/mining.md": "# Mining\n\n## Round results\n",
      ...extra,
    }),
  );
}

test("accepts the documentation foundation and current relative links", () => {
  assert.deepEqual(validateFoundation(foundation()), []);
});

test("rejects broken local destinations and fragments", () => {
  const errors = validateFoundation(
    foundation({
      "docs/README.md":
        "# Guide\n\n[Missing](absent.md) [Wrong section](mining.md#absent)\n",
    }),
  );
  assert.ok(errors.some((error) => error.includes("missing target absent.md")));
  assert.ok(
    errors.some((error) => error.includes("missing heading mining.md#absent")),
  );
});

test("accepts decoded paths and duplicate-heading slugs", () => {
  assert.deepEqual(
    validateFoundation(
      foundation({
        "docs/README.md": "# Guide\n\n[Details](more%20details.md#same-1)\n",
        "docs/more details.md": "# Same\n\n## Same\n",
      }),
    ),
    [],
  );
});

test("rejects links outside the repository and private workspace links", () => {
  const errors = validateFoundation(
    foundation({
      "docs/README.md":
        "# Guide\n\n[Outside](../../private.md) [Private](/home/fc/private.md) [File](file:///private.md)\n",
    }),
  );
  assert.ok(errors.some((error) => error.includes("escapes repository")));
  assert.equal(
    errors.filter((error) => error.includes("private or unsupported link"))
      .length,
    2,
  );
});

test("fails application source and unadmitted CI instead of reporting docs success", () => {
  const errors = validateFoundation(
    foundation({
      "src/app.tsx": "export const app = true;\n",
      "package.json": "{}\n",
      ".github/workflows/other.yml": "name: Other\n",
    }),
  );
  assert.equal(
    errors.filter((error) => error.includes("unsupported tracked path")).length,
    3,
  );
});

test("rejects missing licensing boundaries and malformed documentation", () => {
  const files = foundation({ "docs/README.md": "No title or final newline" });
  files.delete("LICENSE");
  const errors = validateFoundation(files);
  assert.ok(
    errors.some((error) => error.includes("missing foundation file LICENSE")),
  );
  assert.ok(
    errors.some((error) => error.includes("missing top-level heading")),
  );
  assert.ok(errors.some((error) => error.includes("missing final newline")));
});

test("rejects trailing whitespace, conflict markers and malformed URLs", () => {
  const errors = validateFoundation(
    foundation({
      "docs/README.md": "# Guide\n\nText \n<<<<<<< HEAD\n[Broken](https://)\n",
    }),
  );
  assert.ok(errors.some((error) => error.includes("trailing whitespace")));
  assert.ok(errors.some((error) => error.includes("merge conflict marker")));
  assert.ok(errors.some((error) => error.includes("invalid URL")));
});

test("documentation mode checks docs without certifying application source", () => {
  const files = foundation({
    "src/app.tsx": "unverified runtime",
    "web/vendor/NOTICE.md": "Unmodified third-party notice",
    "docs/README.md": "# Guide\n\n[Code](../src/app.tsx)\n",
  });
  assert.deepEqual(validateDocumentation(files), []);
  assert.ok(
    validateFoundation(files).some((e) =>
      e.includes("unsupported tracked path"),
    ),
  );
  files.set("docs/README.md", "# Guide\n\n[Missing](absent.md)\n");
  assert.ok(
    validateDocumentation(files).some((e) => e.includes("missing target")),
  );
  files.delete("LICENSE");
  assert.ok(
    validateDocumentation(files).some((e) =>
      e.includes("missing foundation file LICENSE"),
    ),
  );
});

function mint(extra = {}) {
  return foundation({
    "docs/docs.json": JSON.stringify({
      navigation: { groups: [{ pages: ["index", "guide/chat"] }] },
      logo: { dark: "/assets/logo.svg" },
      redirects: [{ source: "/old", destination: "/guide/chat" }],
    }),
    "docs/index.md":
      '---\ntitle: "Fased"\n---\n\n[Chat](/guide/chat#messages)\n<Card href="/guide/chat">Chat</Card>\n',
    "docs/guide/chat.md":
      '---\ntitle: "Chat"\n---\n\n## Messages\n\n![Logo](/assets/logo.svg)\n',
    "docs/assets/logo.svg": "<svg/>",
    ...extra,
  });
}

test("Mintlify frontmatter, page routes, navigation, redirects and assets resolve", () => {
  assert.deepEqual(validateDocumentation(mint()), []);
  const files = mint();
  files.set("docs/guide/chat.mdx", files.get("docs/guide/chat.md"));
  files.delete("docs/guide/chat.md");
  assert.deepEqual(validateDocumentation(files), []);
});

test("Mintlify rejects missing pages/assets, ambiguous routes, fragments and broken config", () => {
  for (const name of ["docs/guide/chat.md", "docs/assets/logo.svg"]) {
    const files = mint();
    files.delete(name);
    assert.ok(
      validateDocumentation(files).some((e) => e.includes("missing target")),
    );
  }
  assert.ok(
    validateDocumentation(mint({ "docs/guide/chat.mdx": "# Chat\n" })).some(
      (e) => e.includes("ambiguous target"),
    ),
  );
  assert.ok(
    validateDocumentation(
      mint({
        "docs/index.md":
          '---\ntitle: "Fased"\n---\n\n[Chat](/guide/chat#absent)\n',
      }),
    ).some((e) => e.includes("missing heading")),
  );
  assert.ok(
    validateDocumentation(mint({ "docs/docs.json": "{bad" })).some((e) =>
      e.includes("invalid JSON"),
    ),
  );
  assert.ok(
    validateDocumentation(
      mint({
        "docs/docs.json": JSON.stringify({
          navigation: { groups: [{ pages: ["absent"] }] },
        }),
      }),
    ).some((e) => e.includes("missing target absent")),
  );
});

test("Mintlify title support retains malformed title, private-link and traversal checks", () => {
  for (const text of [
    '---\ntitle: ""\n---\n',
    '---\ntitle: "Unclosed"\n',
    "title: Outside\n",
  ])
    assert.ok(
      validateDocumentation(mint({ "docs/index.md": text })).some((e) =>
        e.includes("missing top-level heading"),
      ),
    );
  for (const link of [
    "/home/fc/secret",
    "file:///secret",
    "/%2e%2e/LICENSE",
    "../../LICENSE",
  ]) {
    const errors = validateDocumentation(
      mint({
        "docs/index.md": `---\ntitle: "Fased"\n---\n\n<Card href="${link}">Bad</Card>\n`,
      }),
    );
    assert.ok(
      errors.some((e) => /private or unsupported|escapes repository/.test(e)),
      link,
    );
  }
  assert.ok(
    validateDocumentation(
      foundation({ "README.md": '---\ntitle: "Fased"\n---\n' }),
    ).some((e) => e.includes("missing top-level heading")),
  );
});
