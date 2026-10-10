import { describe, expect, it } from "vitest";
import { dict } from "../i18n/dicts";
import { parseDomainsInput, readTemplatePayload, templatePayloadFromService, templateSummary } from "./services";

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

describe("readTemplatePayload", () => {
  it("accepts an object from the API", () => {
    const p = readTemplatePayload({ domains: [{ name: "a.com", match: "suffix" }], strategy: "weighted", proxies: [] });
    expect(p.strategy).toBe("weighted");
    expect(p.domains).toHaveLength(1);
  });

  it("parses a JSON string", () => {
    const p = readTemplatePayload('{"domains":[{"name":"b.com"}],"strategy":"sticky24","proxies":[]}');
    expect(p.strategy).toBe("sticky24");
    expect(p.domains[0].name).toBe("b.com");
  });

  it("returns an empty payload for junk", () => {
    expect(readTemplatePayload("nope").domains).toEqual([]);
    expect(readTemplatePayload(null).strategy).toBe("round_robin");
  });
});

describe("templatePayloadFromService", () => {
  it("keeps only proxies that are on", () => {
    const p = templatePayloadFromService({
      strategy: "weighted",
      members: [{ name: "a.com", match: "suffix", enabled: true, comment: "" }],
      proxies: [
        { proxy_id: "on", weight: 2, on: true },
        { proxy_id: "off", weight: 9, on: false },
      ],
    });
    expect(p.proxies).toEqual([{ proxy_id: "on", weight: 2 }]);
    expect(p.domains).toEqual([{ name: "a.com", match: "suffix", enabled: true, comment: "" }]);
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
