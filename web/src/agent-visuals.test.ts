import { describe, expect, it } from "vitest";
import {
  parseFasedVisualSpec,
  parseSafeVisualHTML,
  type SafeVisualNode,
} from "./agent-visuals";

function collectText(nodes: SafeVisualNode[]): string {
  return nodes
    .map((node) =>
      node.kind === "text" ? node.text : collectText(node.children),
    )
    .join("");
}

function collectElements(
  nodes: SafeVisualNode[],
): Extract<SafeVisualNode, { kind: "element" }>[] {
  const out: Extract<SafeVisualNode, { kind: "element" }>[] = [];
  for (const node of nodes) {
    if (node.kind === "element") {
      out.push(node, ...collectElements(node.children));
    }
  }
  return out;
}

describe("parseFasedVisualSpec", () => {
  it("parses metrics, bars, table, and sparkline specs cleanly", () => {
    expect(
      parseFasedVisualSpec(
        JSON.stringify({
          type: "metrics",
          title: "Portfolio Snapshot",
          items: [
            {
              label: "SOL",
              value: "$184.20",
              delta: "+4.2%",
              tone: "positive",
            },
            { label: "USDC", value: "$1,250.00" },
          ],
        }),
      ),
    ).toEqual({
      type: "metrics",
      title: "Portfolio Snapshot",
      items: [
        {
          label: "SOL",
          value: "$184.20",
          delta: "+4.2%",
          tone: "positive",
        },
        {
          label: "USDC",
          value: "$1,250.00",
          delta: undefined,
          tone: undefined,
        },
      ],
    });

    expect(
      parseFasedVisualSpec(
        JSON.stringify({
          type: "bars",
          title: "Allocation",
          unit: "%",
          items: [
            { label: "SOL", value: 60, displayValue: "60%" },
            { label: "USDC", value: 40 },
          ],
        }),
      ),
    ).toEqual({
      type: "bars",
      title: "Allocation",
      unit: "%",
      items: [
        { label: "SOL", value: 60, displayValue: "60%" },
        { label: "USDC", value: 40, displayValue: undefined },
      ],
    });

    expect(
      parseFasedVisualSpec(
        JSON.stringify({
          type: "table",
          title: "Recent Quotes",
          columns: ["Pair", "Slippage", "Out"],
          rows: [
            ["SOL/USDC", "50 bps", "184.10"],
            ["JUP/USDC", "30 bps", "1.12"],
          ],
        }),
      ),
    ).toEqual({
      type: "table",
      title: "Recent Quotes",
      columns: ["Pair", "Slippage", "Out"],
      rows: [
        ["SOL/USDC", "50 bps", "184.10"],
        ["JUP/USDC", "30 bps", "1.12"],
      ],
    });

    expect(
      parseFasedVisualSpec(
        JSON.stringify({
          type: "sparkline",
          title: "24h Price",
          unit: "USD",
          values: [176, 179.5, 181, 184.2],
          labels: ["00:00", "08:00", "16:00", "24:00"],
        }),
      ),
    ).toEqual({
      type: "sparkline",
      title: "24h Price",
      unit: "USD",
      values: [176, 179.5, 181, 184.2],
      labels: ["00:00", "08:00", "16:00", "24:00"],
    });
  });

  it("rejects invalid JSON, non-finite series, oversized payloads, and unknown widget types", () => {
    expect(parseFasedVisualSpec("{bad")).toBeNull();
    expect(parseFasedVisualSpec(JSON.stringify({ type: "iframe" }))).toBeNull();
    expect(
      parseFasedVisualSpec(JSON.stringify({ type: "sparkline", values: [1] })),
    ).toBeNull();
    expect(
      parseFasedVisualSpec(
        JSON.stringify({ type: "sparkline", values: [1, "NaN"] }),
      ),
    ).toBeNull();
    expect(parseFasedVisualSpec("x".repeat(40_000))).toBeNull();
  });
});

describe("parseSafeVisualHTML", () => {
  it("preserves safe HTML, safe https links, and geometric SVG primitives", () => {
    const nodes = parseSafeVisualHTML(
      `<section><h4 onclick="alert(1)" style="color:red">Fee Breakdown</h4><p>Priority fee: <strong>0.0002 SOL</strong> <a href="https://docs.solana.com/fees" target="_self" rel="opener">Docs</a></p><svg viewBox="0 0 100 40" width="100" height="40"><path d="M0 30 L50 10 L100 20" stroke="currentColor" stroke-width="2" fill="none" /></svg></section>`,
    );

    const els = collectElements(nodes);
    const h4 = els.find((el) => el.tag === "h4");
    expect(h4?.attrs).toEqual({});

    const link = els.find((el) => el.tag === "a");
    expect(link?.attrs).toEqual({
      href: "https://docs.solana.com/fees",
      target: "_blank",
      rel: "noopener noreferrer",
    });

    const path = els.find((el) => el.tag === "path");
    expect(path?.attrs).toEqual({
      d: "M0 30 L50 10 L100 20",
      stroke: "currentColor",
      strokeWidth: "2",
      fill: "none",
    });
    expect(collectText(nodes)).toContain("Priority fee: 0.0002 SOL");
  });

  it("strips script, style, iframe, foreignObject, entity-encoded javascript hrefs, and SVG url() attrs", () => {
    const nodes = parseSafeVisualHTML(
      `<section>` +
        `<script>window.__xss = 1;</script>` +
        `<style>body { display: none; }</style>` +
        `<iframe src="https://evil.example.com">fallback</iframe>` +
        `<svg viewBox="0 0 10 10"><foreignObject><div>hidden</div></foreignObject><rect x="0" y="0" width="10" height="10" fill="url(#evil)" onload="alert(1)" /></svg>` +
        `<a href="&#106;avascript:alert(1)">BadLink</a>` +
        `<a href="data:text/html,<script>1</script>">DataLink</a>` +
        `</section>`,
    );

    const els = collectElements(nodes);
    const tags = els.map((el) => el.tag);
    expect(tags).not.toContain("script");
    expect(tags).not.toContain("style");
    expect(tags).not.toContain("iframe");
    expect(tags).not.toContain("foreignobject");
    expect(tags).not.toContain("a");

    const rect = els.find((el) => el.tag === "rect");
    expect(rect?.attrs).toEqual({
      x: "0",
      y: "0",
      width: "10",
      height: "10",
    });

    const fullText = collectText(nodes);
    expect(fullText).not.toContain("__xss");
    expect(fullText).not.toContain("display: none");
    expect(fullText).not.toContain("hidden");
    expect(fullText).toContain("BadLink");
    expect(fullText).toContain("DataLink");
  });
});
