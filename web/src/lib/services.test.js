import { describe, expect, it } from "vitest";
import { dict } from "../i18n/dicts";
import { parseDomainsInput, templateSummary } from "./services";

function tFor(lang) {
  return (key, params = {}) =>
    dict[lang][key].replace(/\{(\w+)\}/g, (_, k) => String(params[k]));
}

describe("parseDomainsInput", () => {
  it("splits on newlines, commas and spaces", () => {
    const r = parseDomainsInput("a.com, b.com\nc.com d.com");
    expect(r.ok).toEqual(["a.com", "b.com", "c.com", "d.com"]);
    expect(r.bad).toEqual([]);
  });

  it("reports an invalid name with a reason and keeps the rest", () => {
    const r = parseDomainsInput("good.com a_b.com");
    expect(r.ok).toEqual(["good.com"]);
    expect(r.bad).toEqual([{ raw: "a_b.com", reason: "badLabel" }]);
  });

  it("collapses duplicates and drops the trailing dot and case", () => {
    const r = parseDomainsInput("Example.COM example.com. example.com");
    expect(r.ok).toEqual(["example.com"]);
  });

  it("returns empty lists for blank input", () => {
    expect(parseDomainsInput("  \n ")).toEqual({ ok: [], bad: [] });
  });
});

describe("templateSummary", () => {
  const tpl = { name: "Xbox Live", domains: ["xbox.com", "xboxlive.com"] };

  it("reads in Russian", () => {
    expect(templateSummary(tpl, tFor("ru"))).toBe("шаблон: Xbox Live · 2 доменов");
  });

  it("reads in English", () => {
    expect(templateSummary(tpl, tFor("en"))).toBe("template: Xbox Live · 2 domains");
  });
});
