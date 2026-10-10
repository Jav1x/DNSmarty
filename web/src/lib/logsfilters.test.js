import { describe, it, expect } from "vitest";
import {
  normDomainInput,
  fqdnCheck,
  maskDate,
  parseDay,
  timeRange,
  withinRange,
  histoBuckets,
  activeFilters,
  matchRow,
  auditGroup,
  auditDetail,
} from "./logsfilters";

describe("normDomainInput", () => {
  it("lowercases, strips spaces and keeps the trailing dot", () =>
    expect(normDomainInput(" Example.COM. ")).toBe("example.com."));
  it("collapses double dots", () => expect(normDomainInput("a..b")).toBe("a.b"));
  it("strips leading dots", () => expect(normDomainInput(".example")).toBe("example"));
  it("drops characters outside a domain", () => expect(normDomainInput("ex~ample!")).toBe("example"));
});

describe("fqdnCheck", () => {
  it("empty input is valid without a canonical form", () => {
    const r = fqdnCheck("");
    expect(r.ok).toBe(true);
    expect(r.canonical).toBe("");
  });
  it("appends the canonical trailing dot", () =>
    expect(fqdnCheck("Example.COM")).toEqual({ ok: true, reason: "", canonical: "example.com.", bad: "" }));
  it("keeps an existing trailing dot", () =>
    expect(fqdnCheck("example.com.").canonical).toBe("example.com."));
  it("rejects names over 253 chars", () =>
    expect(fqdnCheck(`${"a".repeat(254)}.com`).reason).toBe("tooLong"));
  it("rejects an empty label", () => expect(fqdnCheck("a..b").reason).toBe("emptyLabel"));
  it("rejects a bad label and names it", () => {
    const r = fqdnCheck("a_b.c");
    expect(r.ok).toBe(false);
    expect(r.reason).toBe("badLabel");
    expect(r.bad).toBe("a_b");
  });
});

describe("maskDate", () => {
  it("passes through up to two digits", () => expect(maskDate("8")).toBe("8"));
  it("puts the first dot after two digits", () => expect(maskDate("810")).toBe("81.0"));
  it("formats DD.MM", () => expect(maskDate("8102")).toBe("81.02"));
  it("formats DD.MM.YYYY", () => expect(maskDate("08102026")).toBe("08.10.2026"));
  it("strips non-digits", () => expect(maskDate("12.04.2026x")).toBe("12.04.2026"));
  it("caps at eight digits", () => expect(maskDate("081020269")).toBe("08.10.2026"));
});

describe("parseDay", () => {
  it("empty is a no-filter sentinel", () => expect(parseDay("")).toEqual({ ok: true, t: null }));
  it("parses DD.MM.YYYY to local midnight", () => {
    const r = parseDay("08.10.2026");
    expect(r.ok).toBe(true);
    expect(r.t.getFullYear()).toBe(2026);
    expect(r.t.getMonth()).toBe(9);
    expect(r.t.getDate()).toBe(8);
    expect(r.t.getHours()).toBe(0);
  });
  it("rejects day 32", () => expect(parseDay("32.10.2026").ok).toBe(false));
  it("rejects month 13", () => expect(parseDay("08.13.2026").ok).toBe(false));
  it("rejects an incomplete mask", () => expect(parseDay("08.10").ok).toBe(false));
  it("rejects an impossible date", () => expect(parseDay("31.02.2026").ok).toBe(false));
});

describe("timeRange", () => {
  it("no preset and no day — no filter", () =>
    expect(timeRange({ preset: "", day: "" }, new Date("2026-10-08T12:00:00"))).toBeNull());
  it("preset 1h starts an hour ago", () => {
    const now = new Date("2026-10-08T12:00:00");
    const r = timeRange({ preset: "1h", day: "" }, now);
    expect(r.start.getTime()).toBe(now.getTime() - 3600_000);
  });
  it("preset 7d starts seven days ago", () => {
    const now = new Date("2026-10-08T12:00:00");
    const r = timeRange({ preset: "7d", day: "" }, now);
    expect(r.start.getTime()).toBe(now.getTime() - 7 * 86400_000);
  });
  it("a parsed day wins over the preset", () => {
    const r = timeRange({ preset: "1h", day: "08.10.2026" }, new Date("2026-10-09T12:00:00"));
    expect(r.start.getDate()).toBe(8);
    expect(r.start.getMonth()).toBe(9);
  });
});

describe("withinRange", () => {
  const range = { start: new Date("2026-10-08T00:00:00Z") };
  it("null range admits everything", () =>
    expect(withinRange("2026-01-01T00:00:00Z", null)).toBe(true));
  it("a row inside the range passes", () =>
    expect(withinRange("2026-10-08T10:00:00Z", range)).toBe(true));
  it("a row before the range fails", () =>
    expect(withinRange("2026-10-07T23:00:00Z", range)).toBe(false));
});

describe("histoBuckets", () => {
  it("no times — no histogram", () => expect(histoBuckets([], 24)).toBeNull());
  it("24 hourly times spread over 24 buckets at full height", () => {
    const base = Date.parse("2026-10-08T00:00:00Z");
    const times = Array.from({ length: 24 }, (_, i) => new Date(base + i * 3600_000).toISOString());
    const h = histoBuckets(times, 24);
    expect(h.heights).toHaveLength(24);
    expect(h.heights.every((v) => v === 100)).toBe(true);
    expect(h.hot).toBe(0);
  });
  it("two clusters land in the first and last buckets", () => {
    const base = Date.parse("2026-10-08T00:00:00Z");
    const times = [
      ...Array.from({ length: 3 }, () => new Date(base).toISOString()),
      ...Array.from({ length: 3 }, () => new Date(base + 3600_000 * 24).toISOString()),
    ];
    const h = histoBuckets(times, 24);
    expect(h.heights[0]).toBe(100);
    expect(h.heights[23]).toBe(100);
    expect(h.heights.reduce((a, b) => a + (b > 0 ? 1 : 0), 0)).toBe(2);
  });
  it("one distinct time is not NaN", () => {
    const h = histoBuckets(["2026-10-08T00:00:00Z", "2026-10-08T00:00:00Z"], 24);
    expect(h.heights[0]).toBe(100);
    expect(h.hot).toBe(0);
  });
});

describe("activeFilters", () => {
  it("serializes every applied dns filter", () =>
    expect(
      activeFilters("dns", {
        q: "example.com", ip: "198.51.100.0/24", decision: "acl",
        status: "", qtype: "A", rcode: "", preset: "24h", day: "",
      }),
    ).toEqual([
      { k: "q", v: "example.com" },
      { k: "ip", v: "198.51.100.0/24" },
      { k: "decision", v: "acl" },
      { k: "qtype", v: "A" },
      { k: "time", v: "24h" },
    ]));
  it("serializes the proxy status instead of the dns decision", () =>
    expect(
      activeFilters("proxy", {
        q: "", ip: "192.0.2.7", decision: "", status: "dial_error",
        qtype: "", rcode: "", preset: "", day: "08.10.2026",
      }),
    ).toEqual([
      { k: "ip", v: "192.0.2.7" },
      { k: "status", v: "dial_error" },
      { k: "time", v: "08.10.2026" },
    ]));
  it("empty applied — no chips", () =>
    expect(
      activeFilters("dns", {
        q: "", ip: "", decision: "", status: "", qtype: "", rcode: "", preset: "", day: "",
      }),
    ).toEqual([]));
});

describe("matchRow", () => {
  const row = { qtype: "A", rcode: "NOERROR", at: "2026-10-08T10:00:00Z" };
  const range = { start: new Date("2026-10-08T00:00:00Z") };
  it("no client filters — everything passes", () =>
    expect(matchRow(row, "", "", null)).toBe(true));
  it("qtype mismatch fails", () => expect(matchRow(row, "MX", "", null)).toBe(false));
  it("rcode mismatch fails", () => expect(matchRow(row, "", "NXDOMAIN", null)).toBe(false));
  it("a row older than the range fails", () =>
    expect(matchRow({ ...row, at: "2026-10-07T10:00:00Z" }, "", "", range)).toBe(false));
  it("all filters together", () =>
    expect(matchRow(row, "A", "NOERROR", range)).toBe(true));
});

describe("auditGroup", () => {
  it("login family", () => {
    expect(auditGroup("login")).toBe("login");
    expect(auditGroup("login.fail")).toBe("login");
    expect(auditGroup("logout")).toBe("login");
  });
  it("known prefixes", () => {
    expect(auditGroup("node.check")).toBe("node");
    expect(auditGroup("session.revoke_others")).toBe("session");
    expect(auditGroup("service.update")).toBe("service");
    expect(auditGroup("settings.save")).toBe("settings");
    expect(auditGroup("acl.create")).toBe("acl");
  });
  it("unknown action — other", () => expect(auditGroup("mystery")).toBe("other"));
});

describe("auditDetail", () => {
  it("login detail is the ip", () =>
    expect(auditDetail("login", '{"ip":"203.0.113.9"}')).toEqual([{ k: "ip", v: "203.0.113.9" }]));
  it("node.check detail is id and ok", () =>
    expect(auditDetail("node.check", '{"id":5,"ok":true}')).toEqual([
      { k: "id", v: 5 },
      { k: "ok", v: true },
    ]));
  it("empty detail — no rows", () => {
    expect(auditDetail("logout", "{}")).toEqual([]);
    expect(auditDetail("logout", "")).toEqual([]);
  });
  it("unparseable detail falls back to the raw text", () =>
    expect(auditDetail("login", "не json")).toEqual([{ k: "raw", v: "не json" }]));
});
