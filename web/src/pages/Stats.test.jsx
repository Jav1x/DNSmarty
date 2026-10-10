import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { I18nProvider } from "../i18n";
import { Stats } from "./Stats.jsx";

const payload = {
  window: "24h",
  totals: { queries: 100, blocked: 10, clients: 2, sessions: 4, bytes: 2048 },
  domains: [
    { name: "a.example", queries: 80, acl: 8, clients: 2 },
    { name: "b.example", queries: 20, acl: 2, clients: 1 },
  ],
  proxy: [
    { name: "cdn.example", sessions: 3, bytes: 1500 },
    { name: "api.example", sessions: 1, bytes: 548 },
  ],
  clients: [
    { ip: "10.0.0.1", queries: 70, acl: 7 },
    { ip: "10.0.0.2", queries: 30, acl: 3 },
  ],
};

function jsonOk(data) {
  return Promise.resolve({
    ok: true,
    status: 200,
    text: async () => JSON.stringify(data),
  });
}

async function mount() {
  const host = document.createElement("div");
  document.body.appendChild(host);
  let root;
  await act(async () => {
    root = createRoot(host);
    root.render(
      <I18nProvider>
        <Stats />
      </I18nProvider>,
    );
  });
  return {
    host,
    unmount: () => act(() => { root.unmount(); host.remove(); }),
  };
}

describe("Stats rewrite (spec §5.4)", () => {
  const prevFetch = globalThis.fetch;
  const calls = [];

  beforeEach(() => {
    localStorage.setItem("dnsmarty-lang", "en");
    calls.length = 0;
    globalThis.fetch = async (url) => {
      calls.push(String(url));
      const u = String(url);
      if (u.startsWith("/api/stats/client")) {
        return jsonOk({ rows: [{ name: "a.example", queries: 70, acl: 7 }] });
      }
      if (u.startsWith("/api/stats")) return jsonOk(payload);
      throw new Error(`unexpected fetch ${url}`);
    };
  });

  afterEach(() => {
    globalThis.fetch = prevFetch;
    localStorage.removeItem("dnsmarty-lang");
  });

  it("renders the window totals and the three tables", async () => {
    const { host, unmount } = await mount();
    expect(host.querySelector(".hero.st").textContent).toContain("100");
    expect(host.querySelector(".hero.st").textContent).toContain("10");
    expect(host.querySelector(".hero.st").textContent).toContain("2.0 KB");
    const cards = host.querySelectorAll(".stats-grid .tablecard");
    expect(cards).toHaveLength(3);
    expect(cards[0].textContent).toContain("a.example");
    expect(cards[1].textContent).toContain("cdn.example");
    expect(cards[2].textContent).toContain("10.0.0.1");
    unmount();
  });

  it("opens the client drill-down", async () => {
    const { host, unmount } = await mount();
    await act(async () => {
      const row = [...host.querySelectorAll(".stats-grid .full tbody tr")].find((tr) => tr.textContent.includes("10.0.0.1"));
      row.click();
    });
    expect(host.querySelector(".drill b").textContent).toContain("10.0.0.1");
    expect(calls.some((u) => u.includes("/api/stats/client?ip=10.0.0.1"))).toBe(true);
    unmount();
  });

  it("sorts the domain table when a header is clicked", async () => {
    const { host, unmount } = await mount();
    const names = () => [...host.querySelectorAll(".stats-grid .tablecard:first-child tbody tr")].map((tr) => tr.children[1].textContent);
    expect(names()).toEqual(["a.example", "b.example"]);
    await act(async () => {
      [...host.querySelectorAll(".stats-grid .tablecard:first-child th")].find((th) => th.textContent.includes("Domain")).click();
    });
    expect(names()).toEqual(["b.example", "a.example"]);
    unmount();
  });
});
