import { isSafePublicHttpsUrl } from "./agents-v2";

const MAX_VISUAL_RAW_CHARS = 32_768;
const MAX_SAFE_HTML_DEPTH = 16;
const MAX_SAFE_HTML_NODES = 400;

export type VisualMetricItem = {
  label: string;
  value: string;
  delta?: string;
  tone?: "positive" | "negative" | "neutral";
};

export type VisualBarItem = {
  label: string;
  value: number;
  displayValue?: string;
};

export type FasedVisualSpec =
  | {
      type: "metrics";
      title?: string;
      items: VisualMetricItem[];
    }
  | {
      type: "bars";
      title?: string;
      unit?: string;
      items: VisualBarItem[];
    }
  | {
      type: "table";
      title?: string;
      columns: string[];
      rows: string[][];
    }
  | {
      type: "sparkline";
      title?: string;
      unit?: string;
      values: number[];
      labels?: string[];
    };

export type SafeVisualNode =
  | { kind: "text"; text: string }
  | {
      kind: "element";
      tag: string;
      attrs: Record<string, string | number>;
      children: SafeVisualNode[];
    };

function trimText(value: unknown, maxLen: number): string {
  if (typeof value !== "string" && typeof value !== "number") return "";
  return String(value).replace(/\s+/g, " ").trim().slice(0, maxLen);
}

export function parseFasedVisualSpec(raw: string): FasedVisualSpec | null {
  if (!raw || raw.length > MAX_VISUAL_RAW_CHARS) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return null;
  }
  const obj = parsed as Record<string, unknown>;
  const type =
    typeof obj.type === "string" ? obj.type.trim().toLowerCase() : "";
  const title = trimText(obj.title, 120) || undefined;

  if (type === "metrics") {
    if (!Array.isArray(obj.items) || obj.items.length === 0) return null;
    const items: VisualMetricItem[] = [];
    for (const rawItem of obj.items.slice(0, 12)) {
      if (!rawItem || typeof rawItem !== "object") continue;
      const item = rawItem as Record<string, unknown>;
      const label = trimText(item.label, 80);
      const value = trimText(item.value, 80);
      if (!label || !value) continue;
      const delta = trimText(item.delta, 40) || undefined;
      const rawTone =
        typeof item.tone === "string" ? item.tone.trim().toLowerCase() : "";
      const tone =
        rawTone === "positive" ||
        rawTone === "negative" ||
        rawTone === "neutral"
          ? rawTone
          : undefined;
      items.push({ label, value, delta, tone });
    }
    return items.length > 0 ? { type: "metrics", title, items } : null;
  }

  if (type === "bars") {
    if (!Array.isArray(obj.items) || obj.items.length === 0) return null;
    const unit = trimText(obj.unit, 24) || undefined;
    const items: VisualBarItem[] = [];
    for (const rawItem of obj.items.slice(0, 20)) {
      if (!rawItem || typeof rawItem !== "object") continue;
      const item = rawItem as Record<string, unknown>;
      const label = trimText(item.label, 80);
      const num =
        typeof item.value === "number" ? item.value : Number(item.value);
      if (!label || !Number.isFinite(num)) continue;
      const displayValue = trimText(item.displayValue, 60) || undefined;
      items.push({ label, value: num, displayValue });
    }
    return items.length > 0 ? { type: "bars", title, unit, items } : null;
  }

  if (type === "table") {
    if (!Array.isArray(obj.columns) || obj.columns.length === 0) return null;
    const columns = obj.columns
      .slice(0, 8)
      .map((c) => trimText(c, 80))
      .filter(Boolean);
    if (columns.length === 0) return null;
    const rawRows = Array.isArray(obj.rows) ? obj.rows : [];
    const rows: string[][] = [];
    for (const rawRow of rawRows.slice(0, 30)) {
      if (!Array.isArray(rawRow)) continue;
      const cells = columns.map((_, idx) => trimText(rawRow[idx], 160));
      rows.push(cells);
    }
    return { type: "table", title, columns, rows };
  }

  if (type === "sparkline") {
    if (!Array.isArray(obj.values) || obj.values.length < 2) return null;
    const unit = trimText(obj.unit, 24) || undefined;
    const values: number[] = [];
    for (const v of obj.values.slice(0, 60)) {
      const num = typeof v === "number" ? v : Number(v);
      if (!Number.isFinite(num)) return null;
      values.push(num);
    }
    if (values.length < 2) return null;
    const labels = Array.isArray(obj.labels)
      ? obj.labels.slice(0, values.length).map((l) => trimText(l, 40))
      : undefined;
    return { type: "sparkline", title, unit, values, labels };
  }

  return null;
}

const ALLOWED_HTML_TAGS = new Set([
  "div",
  "section",
  "article",
  "header",
  "footer",
  "p",
  "span",
  "strong",
  "em",
  "b",
  "i",
  "small",
  "code",
  "pre",
  "ul",
  "ol",
  "li",
  "h3",
  "h4",
  "h5",
  "table",
  "thead",
  "tbody",
  "tr",
  "th",
  "td",
  "dl",
  "dt",
  "dd",
  "hr",
  "br",
  "a",
  "figure",
  "figcaption",
]);

const ALLOWED_SVG_TAGS = new Set([
  "svg",
  "g",
  "path",
  "rect",
  "circle",
  "ellipse",
  "line",
  "polyline",
  "polygon",
  "text",
  "tspan",
]);

const VOID_TAGS = new Set([
  "br",
  "hr",
  "img",
  "input",
  "meta",
  "link",
  "base",
  "embed",
  "param",
  "source",
  "track",
  "wbr",
  "area",
  "col",
]);

const DROP_SUBTREE_TAGS = new Set([
  "script",
  "style",
  "iframe",
  "object",
  "embed",
  "form",
  "input",
  "button",
  "textarea",
  "select",
  "option",
  "link",
  "meta",
  "base",
  "foreignobject",
  "animate",
  "set",
  "noscript",
  "template",
]);

const SVG_ATTR_PROP_MAP: Record<string, string> = {
  viewbox: "viewBox",
  width: "width",
  height: "height",
  x: "x",
  y: "y",
  x1: "x1",
  y1: "y1",
  x2: "x2",
  y2: "y2",
  cx: "cx",
  cy: "cy",
  r: "r",
  rx: "rx",
  ry: "ry",
  d: "d",
  points: "points",
  fill: "fill",
  stroke: "stroke",
  "stroke-width": "strokeWidth",
  "stroke-linecap": "strokeLinecap",
  "stroke-linejoin": "strokeLinejoin",
  opacity: "opacity",
  "text-anchor": "textAnchor",
  "font-size": "fontSize",
};

function decodeHtmlEntities(input: string): string {
  return input.replace(/&(#x?[0-9a-fA-F]+|[a-zA-Z]+);/g, (full, entity) => {
    const lower = entity.toLowerCase();
    if (lower === "amp") return "&";
    if (lower === "lt") return "<";
    if (lower === "gt") return ">";
    if (lower === "quot") return '"';
    if (lower === "apos" || lower === "#39") return "'";
    if (lower === "nbsp") return " ";
    if (lower.startsWith("#x")) {
      const code = Number.parseInt(lower.slice(2), 16);
      if (Number.isFinite(code) && code > 0 && code <= 0x10ffff) {
        return String.fromCodePoint(code);
      }
      return "";
    }
    if (lower.startsWith("#")) {
      const code = Number.parseInt(lower.slice(1), 10);
      if (Number.isFinite(code) && code > 0 && code <= 0x10ffff) {
        return String.fromCodePoint(code);
      }
      return "";
    }
    return full;
  });
}

function isSafeSvgAttrValue(prop: string, rawVal: string): boolean {
  const val = rawVal.trim();
  if (!val || val.length > 2048) return false;
  const lower = val.toLowerCase();
  if (
    lower.includes("url(") ||
    lower.includes("javascript:") ||
    lower.includes("vbscript:") ||
    lower.includes("data:") ||
    lower.includes("expression(") ||
    val.includes("<") ||
    val.includes(">")
  ) {
    return false;
  }
  if (prop === "d") {
    return /^[a-zA-Z0-9.,+\-\s]+$/.test(val);
  }
  if (prop === "viewBox" || prop === "points") {
    return /^[0-9.,+\-\s]+$/.test(val);
  }
  if (prop === "fill" || prop === "stroke") {
    return /^(?:none|currentcolor|transparent|#[0-9a-f]{3,8}|[a-z]{3,24})$/i.test(
      val,
    );
  }
  if (prop === "strokeLinecap" || prop === "strokeLinejoin") {
    return /^(?:butt|round|square|miter|bevel)$/i.test(val);
  }
  if (prop === "textAnchor") {
    return /^(?:start|middle|end)$/i.test(val);
  }
  return /^-?\d+(?:\.\d+)?(?:%|px|em|rem)?$/.test(val);
}

function parseRawAttributes(rawAttrs: string): Record<string, string> {
  const result: Record<string, string> = {};
  const attrRe =
    /([a-zA-Z_:][a-zA-Z0-9_.:-]*)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g;
  let match: RegExpExecArray | null;
  while ((match = attrRe.exec(rawAttrs)) !== null) {
    const key = match[1].toLowerCase();
    const rawVal = match[2] ?? match[3] ?? match[4] ?? "";
    result[key] = decodeHtmlEntities(rawVal);
  }
  return result;
}

function sanitizeElementAttrs(
  tag: string,
  rawAttrs: Record<string, string>,
): { tag: string; attrs: Record<string, string | number> } {
  const attrs: Record<string, string | number> = {};

  if (tag === "a") {
    const href = (rawAttrs.href ?? "").trim();
    if (isSafePublicHttpsUrl(href)) {
      attrs.href = href;
      attrs.target = "_blank";
      attrs.rel = "noopener noreferrer";
      return { tag: "a", attrs };
    }
    return { tag: "span", attrs: {} };
  }

  if (tag === "td" || tag === "th") {
    for (const [attrKey, propKey] of [
      ["colspan", "colSpan"],
      ["rowspan", "rowSpan"],
    ] as const) {
      if (rawAttrs[attrKey] !== undefined) {
        const span = Number.parseInt(rawAttrs[attrKey], 10);
        if (Number.isFinite(span) && span >= 1 && span <= 8) {
          attrs[propKey] = span;
        }
      }
    }
    return { tag, attrs };
  }

  if (ALLOWED_SVG_TAGS.has(tag)) {
    for (const [k, v] of Object.entries(rawAttrs)) {
      const mapped = SVG_ATTR_PROP_MAP[k];
      if (!mapped) continue;
      if (isSafeSvgAttrValue(mapped, v)) {
        attrs[mapped] = v.trim();
      }
    }
    return { tag, attrs };
  }

  return { tag, attrs };
}

function readTagEndIndex(source: string, ltIndex: number): number {
  let quote: '"' | "'" | null = null;
  for (let i = ltIndex + 1; i < source.length; i++) {
    const ch = source[i];
    if (quote) {
      if (ch === quote) quote = null;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      continue;
    }
    if (ch === ">") return i;
  }
  return -1;
}

type StackFrame = {
  origTag: string;
  children: SafeVisualNode[];
};

export function parseSafeVisualHTML(raw: string): SafeVisualNode[] {
  if (!raw || raw.length > MAX_VISUAL_RAW_CHARS) return [];

  const rootChildren: SafeVisualNode[] = [];
  const stack: StackFrame[] = [{ origTag: "__root__", children: rootChildren }];
  const dropStack: string[] = [];
  let nodeCount = 0;
  let pos = 0;

  while (pos < raw.length && nodeCount < MAX_SAFE_HTML_NODES) {
    const lt = raw.indexOf("<", pos);
    if (lt === -1) {
      if (dropStack.length === 0) {
        const tail = decodeHtmlEntities(raw.slice(pos));
        if (tail) {
          stack[stack.length - 1].children.push({ kind: "text", text: tail });
          nodeCount++;
        }
      }
      break;
    }

    if (lt > pos && dropStack.length === 0) {
      const text = decodeHtmlEntities(raw.slice(pos, lt));
      if (text) {
        stack[stack.length - 1].children.push({ kind: "text", text });
        nodeCount++;
      }
    }

    if (raw.startsWith("<!--", lt)) {
      const endComment = raw.indexOf("-->", lt + 4);
      pos = endComment === -1 ? raw.length : endComment + 3;
      continue;
    }

    if (raw.startsWith("<!", lt) || raw.startsWith("<?", lt)) {
      const gt = raw.indexOf(">", lt + 2);
      pos = gt === -1 ? raw.length : gt + 1;
      continue;
    }

    const gt = readTagEndIndex(raw, lt);
    if (gt === -1) break;

    const inside = raw.slice(lt + 1, gt).trim();
    pos = gt + 1;
    if (!inside) continue;

    if (inside.startsWith("/")) {
      const closeTag = inside.slice(1).trim().split(/\s+/)[0].toLowerCase();
      if (dropStack.length > 0) {
        const idx = dropStack.lastIndexOf(closeTag);
        if (idx !== -1) dropStack.length = idx;
        continue;
      }
      for (let i = stack.length - 1; i >= 1; i--) {
        if (stack[i].origTag === closeTag) {
          stack.length = i;
          break;
        }
      }
      continue;
    }

    if (dropStack.length > 0) {
      const mInner = /^([a-zA-Z][a-zA-Z0-9-]*)/.exec(inside);
      if (mInner && !inside.endsWith("/")) {
        const innerTag = mInner[1].toLowerCase();
        if (innerTag === dropStack[dropStack.length - 1]) {
          dropStack.push(innerTag);
        }
      }
      continue;
    }

    const tagMatch = /^([a-zA-Z][a-zA-Z0-9-]*)/.exec(inside);
    if (!tagMatch) continue;
    const origTag = tagMatch[1].toLowerCase();
    const selfClosing = inside.endsWith("/") || VOID_TAGS.has(origTag);

    if (DROP_SUBTREE_TAGS.has(origTag)) {
      if (!selfClosing && !VOID_TAGS.has(origTag)) {
        if (origTag === "script" || origTag === "style") {
          const closeRe = new RegExp(`</\\s*${origTag}\\b[^>]*>`, "i");
          const rest = raw.slice(pos);
          const found = closeRe.exec(rest);
          pos = found ? pos + found.index + found[0].length : raw.length;
        } else {
          dropStack.push(origTag);
        }
      }
      continue;
    }

    const isAllowed =
      ALLOWED_HTML_TAGS.has(origTag) || ALLOWED_SVG_TAGS.has(origTag);
    if (!isAllowed) {
      continue;
    }

    if (stack.length - 1 >= MAX_SAFE_HTML_DEPTH) {
      continue;
    }

    let rawAttrStr = inside.slice(tagMatch[0].length);
    if (rawAttrStr.endsWith("/")) {
      rawAttrStr = rawAttrStr.slice(0, -1);
    }
    const rawAttrs = parseRawAttributes(rawAttrStr);
    const { tag: cleanTag, attrs } = sanitizeElementAttrs(origTag, rawAttrs);
    const children: SafeVisualNode[] = [];
    stack[stack.length - 1].children.push({
      kind: "element",
      tag: cleanTag,
      attrs,
      children,
    });
    nodeCount++;

    if (!selfClosing) {
      stack.push({ origTag, children });
    }
  }

  return rootChildren;
}
