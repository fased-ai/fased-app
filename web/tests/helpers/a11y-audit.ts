import type { Page } from "@playwright/test";

export interface A11yViolation {
  rule: string;
  severity: "error" | "warning";
  selector: string;
  detail: string;
}

export interface A11yAuditReport {
  violations: A11yViolation[];
  checkedElements: number;
  contrastChecked: number;
}

export async function auditPageAccessibility(
  page: Page,
  options: { checkContrast?: boolean } = {},
): Promise<A11yAuditReport> {
  const { checkContrast = true } = options;
  return page.evaluate((runContrast) => {
    const violations: Array<{
      rule: string;
      severity: "error" | "warning";
      selector: string;
      detail: string;
    }> = [];

    const describeEl = (el: Element): string => {
      const tag = el.tagName.toLowerCase();
      const id = el.id ? `#${el.id}` : "";
      const cls =
        typeof el.className === "string" && el.className.trim()
          ? "." + el.className.trim().split(/\s+/).slice(0, 2).join(".")
          : "";
      const text = (el.textContent || "")
        .trim()
        .replace(/\s+/g, " ")
        .slice(0, 40);
      return `${tag}${id}${cls}${text ? `("${text}")` : ""}`;
    };

    const isVisible = (el: Element): boolean => {
      const style = window.getComputedStyle(el);
      if (
        style.display === "none" ||
        style.visibility === "hidden" ||
        parseFloat(style.opacity || "1") === 0
      ) {
        return false;
      }
      if (el.closest('[aria-hidden="true"]')) {
        return false;
      }
      const rect = el.getBoundingClientRect();
      return rect.width > 0.5 && rect.height > 0.5;
    };

    const accessibleName = (el: Element): string => {
      const ariaLabel = el.getAttribute("aria-label")?.trim();
      if (ariaLabel) return ariaLabel;
      const labelledBy = el.getAttribute("aria-labelledby")?.trim();
      if (labelledBy) {
        const parts = labelledBy
          .split(/\s+/)
          .map((id) => document.getElementById(id)?.textContent?.trim() || "")
          .filter(Boolean);
        if (parts.length > 0) return parts.join(" ");
      }
      if (
        el instanceof HTMLInputElement ||
        el instanceof HTMLTextAreaElement ||
        el instanceof HTMLSelectElement
      ) {
        if (el.id) {
          const forLabel = document.querySelector(
            `label[for="${CSS.escape(el.id)}"]`,
          );
          if (forLabel?.textContent?.trim()) return forLabel.textContent.trim();
        }
        const ancestorLabel = el.closest("label");
        if (ancestorLabel?.textContent?.trim())
          return ancestorLabel.textContent.trim();
        if ("placeholder" in el && el.placeholder?.trim())
          return el.placeholder.trim();
      }
      const title = el.getAttribute("title")?.trim();
      if (title) return title;
      const text = (el.textContent || "").trim().replace(/\s+/g, " ");
      if (text) return text;
      const labelledChild = el.querySelector("[aria-label], img[alt]");
      if (labelledChild) {
        return (
          labelledChild.getAttribute("aria-label") ||
          labelledChild.getAttribute("alt") ||
          ""
        ).trim();
      }
      return "";
    };

    // 1. Document language and title
    if (!document.documentElement.lang?.trim()) {
      violations.push({
        rule: "html-has-lang",
        severity: "error",
        selector: "html",
        detail: "Document root missing non-empty lang attribute",
      });
    }
    if (!document.title?.trim()) {
      violations.push({
        rule: "document-title",
        severity: "error",
        selector: "title",
        detail: "Document missing non-empty title",
      });
    }

    // 2. Unique IDs
    const seenIds = new Map<string, number>();
    for (const el of document.querySelectorAll("[id]")) {
      if (!el.id) continue;
      seenIds.set(el.id, (seenIds.get(el.id) || 0) + 1);
    }
    for (const [id, count] of seenIds) {
      if (count > 1) {
        violations.push({
          rule: "duplicate-id",
          severity: "error",
          selector: `#${id}`,
          detail: `Duplicate id="${id}" occurs ${count} times`,
        });
      }
    }

    // 3. Images missing alt
    for (const img of document.querySelectorAll("img")) {
      if (
        img.getAttribute("aria-hidden") === "true" ||
        img.getAttribute("role") === "presentation" ||
        img.getAttribute("role") === "none"
      ) {
        continue;
      }
      if (!img.hasAttribute("alt")) {
        violations.push({
          rule: "image-alt",
          severity: "error",
          selector: describeEl(img),
          detail: "<img> element missing alt attribute",
        });
      }
    }

    // 4. Visible buttons, links, form controls, nested interactive, and dialogs
    let checkedElements = 0;
    for (const el of document.querySelectorAll(
      'button, a[href], [role="button"], [role="link"]',
    )) {
      if (!isVisible(el)) continue;
      checkedElements++;
      if (!accessibleName(el)) {
        violations.push({
          rule: "interactive-name",
          severity: "error",
          selector: describeEl(el),
          detail: "Interactive control has no accessible name",
        });
      }
      const nested = el.querySelector("button, a[href]");
      if (nested) {
        violations.push({
          rule: "nested-interactive",
          severity: "error",
          selector: describeEl(el),
          detail: `Nested interactive element ${describeEl(nested)}`,
        });
      }
    }

    for (const input of document.querySelectorAll("input, select, textarea")) {
      if (
        input instanceof HTMLInputElement &&
        (input.type === "hidden" || input.disabled)
      ) {
        continue;
      }
      if (!isVisible(input)) continue;
      checkedElements++;
      if (!accessibleName(input)) {
        violations.push({
          rule: "form-label",
          severity: "error",
          selector: describeEl(input),
          detail: "Form control missing label or aria-label",
        });
      }
    }

    // 5. aria-hidden focusable descendants
    for (const hidden of document.querySelectorAll('[aria-hidden="true"]')) {
      for (const child of hidden.querySelectorAll(
        'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
      )) {
        const ti = child.getAttribute("tabindex");
        if (ti === "-1") continue;
        const style = window.getComputedStyle(child);
        if (style.display === "none" || style.visibility === "hidden") continue;
        violations.push({
          rule: "aria-hidden-focus",
          severity: "error",
          selector: describeEl(child),
          detail:
            'Focusable element exists inside aria-hidden="true" container',
        });
      }
    }

    // 6. Visible dialogs have ARIA modal and accessible name
    for (const dialog of document.querySelectorAll('[role="dialog"]')) {
      if (!isVisible(dialog)) continue;
      checkedElements++;
      if (dialog.getAttribute("aria-modal") !== "true") {
        violations.push({
          rule: "dialog-modal",
          severity: "error",
          selector: describeEl(dialog),
          detail: 'Visible role="dialog" missing aria-modal="true"',
        });
      }
      if (!accessibleName(dialog)) {
        violations.push({
          rule: "dialog-name",
          severity: "error",
          selector: describeEl(dialog),
          detail: 'Visible role="dialog" missing accessible name',
        });
      }
    }

    // 7. WCAG 2.1 AA Contrast verification
    let contrastChecked = 0;
    if (runContrast) {
      type RGBA = { r: number; g: number; b: number; a: number };
      const parseColor = (raw: string): RGBA | null => {
        const m = raw.match(
          /rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)(?:\s*,\s*([\d.]+))?\s*\)/i,
        );
        if (!m) return null;
        return {
          r: Number(m[1]),
          g: Number(m[2]),
          b: Number(m[3]),
          a: m[4] === undefined ? 1 : Number(m[4]),
        };
      };
      const blendOver = (fg: RGBA, bg: RGBA): RGBA => {
        const a = fg.a;
        return {
          r: fg.r * a + bg.r * (1 - a),
          g: fg.g * a + bg.g * (1 - a),
          b: fg.b * a + bg.b * (1 - a),
          a: 1,
        };
      };
      const luminance = (c: RGBA): number => {
        const channel = (v: number) => {
          const s = v / 255;
          return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
        };
        return (
          0.2126 * channel(c.r) + 0.7152 * channel(c.g) + 0.0722 * channel(c.b)
        );
      };
      const contrastRatio = (c1: RGBA, c2: RGBA): number => {
        const l1 = luminance(c1);
        const l2 = luminance(c2);
        const [bright, dark] = l1 >= l2 ? [l1, l2] : [l2, l1];
        return (bright + 0.05) / (dark + 0.05);
      };
      const effectiveBackground = (el: Element): RGBA => {
        const chain: RGBA[] = [];
        let cur: Element | null = el;
        while (cur) {
          const bg = parseColor(window.getComputedStyle(cur).backgroundColor);
          if (bg && bg.a > 0.01) {
            chain.push(bg);
            if (bg.a >= 0.999) break;
          }
          cur = cur.parentElement;
        }
        const theme = document.documentElement.dataset.theme;
        let acc: RGBA =
          theme === "light"
            ? { r: 255, g: 255, b: 255, a: 1 }
            : { r: 17, g: 17, b: 20, a: 1 };
        for (let i = chain.length - 1; i >= 0; i--) {
          acc = blendOver(chain[i], acc);
        }
        return acc;
      };

      const walker = document.createTreeWalker(
        document.body,
        NodeFilter.SHOW_TEXT,
      );
      const checkedNodes = new Set<Element>();
      let node: Node | null;
      while ((node = walker.nextNode())) {
        if (!node.textContent || !node.textContent.trim()) continue;
        const el = node.parentElement;
        if (!el || checkedNodes.has(el)) continue;
        checkedNodes.add(el);
        if (!isVisible(el)) continue;
        if (
          (el as HTMLButtonElement).disabled ||
          el.closest("[disabled], .is-disabled")
        ) {
          continue;
        }
        const style = window.getComputedStyle(el);
        const fg = parseColor(style.color);
        if (!fg || fg.a < 0.05) continue;
        const bg = effectiveBackground(el);
        const compositedFg = fg.a < 0.999 ? blendOver(fg, bg) : fg;
        const ratio = contrastRatio(compositedFg, bg);
        const fontSize = parseFloat(style.fontSize || "14");
        const fontWeight = parseInt(style.fontWeight || "400", 10) || 400;
        const isLarge =
          fontSize >= 24 || (fontSize >= 18.67 && fontWeight >= 700);
        const required = isLarge ? 3.0 : 4.5;
        contrastChecked++;
        if (ratio < required) {
          violations.push({
            rule: "color-contrast",
            severity: "error",
            selector: describeEl(el),
            detail: `Contrast ${ratio.toFixed(2)}:1 < ${required}:1 (fg=${style.color}, bg=rgb(${Math.round(bg.r)},${Math.round(bg.g)},${Math.round(bg.b)}), size=${fontSize}px, weight=${fontWeight})`,
          });
        }
      }
    }

    return { violations, checkedElements, contrastChecked };
  }, checkContrast);
}
