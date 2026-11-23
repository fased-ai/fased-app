import { describe, expect, it } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { Disclosure, ToggleSwitch } from "./shared";

describe("shared WEN primitives", () => {
  it("renders Disclosure closed and open states without leaking body when closed", () => {
    const noop = () => {};
    const closed = renderToStaticMarkup(
      createElement(Disclosure, {
        label: "API key",
        dataSection: "rpc-api-key",
        open: false,
        onOpenChange: noop,
        children: createElement("p", null, "Hidden secret form"),
      }),
    );
    expect(closed).toContain('aria-expanded="false"');
    expect(closed).toContain('data-open="false"');
    expect(closed).toContain('data-section="rpc-api-key"');
    expect(closed).toContain("<span>API key</span>");
    expect(closed).not.toContain("Hidden secret form");

    const opened = renderToStaticMarkup(
      createElement(Disclosure, {
        label: "API key",
        dataSection: "rpc-api-key",
        open: true,
        onOpenChange: noop,
        children: createElement("p", null, "Visible form"),
      }),
    );
    expect(opened).toContain('aria-expanded="true"');
    expect(opened).toContain('data-open="true"');
    expect(opened).toContain("Visible form");
  });

  it("renders ToggleSwitch with native checkbox semantics and checked state", () => {
    const noop = () => {};
    const unchecked = renderToStaticMarkup(
      createElement(ToggleSwitch, {
        checked: false,
        onChange: noop,
        label: "Show in inbox",
      }),
    );
    expect(unchecked).toContain('type="checkbox"');
    expect(unchecked).toContain("Show in inbox");
    expect(unchecked).not.toContain("checked");

    const checked = renderToStaticMarkup(
      createElement(ToggleSwitch, {
        checked: true,
        onChange: noop,
        label: "Maintenance window",
      }),
    );
    expect(checked).toContain("checked");
    expect(checked).toContain("Maintenance window");
  });
});
