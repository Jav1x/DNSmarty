import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { I18nProvider } from "../i18n";
import { ToastProvider } from "../components/Toast";
import { Services } from "./Services.jsx";

const payload = {
  services: [
    {
      id: "svc-1",
      name: "Steam",
      strategy: "weighted",
      enabled: true,
      comment: "games",
      queries_24h: 12,
      bytes_24h: 2048,
      members: [
        { id: "m1", name: "steam.com", match: "suffix", enabled: true, comment: "", queries_24h: 8 },
        { id: "m2", name: "steampowered.com", match: "fqdn", enabled: false, comment: "", queries_24h: 4 },
      ],
      proxies: [
        { proxy_id: "n1", proxy_name: "edge-1", weight: 2, on: true },
        { proxy_id: "n2", proxy_name: "edge-2", weight: 1, on: true },
      ],
    },
    {
      id: "svc-2",
      name: "Idle",
      strategy: "round_robin",
      enabled: false,
      comment: "",
      queries_24h: 0,
      bytes_24h: 0,
      members: [],
      proxies: [],
    },
  ],
  proxies: [
    { id: "n1", name: "edge-1" },
    { id: "n2", name: "edge-2" },
  ],
  templates: [],
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
        <ToastProvider>
          <Services />
        </ToastProvider>
      </I18nProvider>,
    );
  });
  return {
    host,
    unmount: () => act(() => { root.unmount(); host.remove(); }),
  };
}

describe("Services cards (spec §5.7)", () => {
  const prevFetch = globalThis.fetch;

  beforeEach(() => {
    localStorage.setItem("dnsmarty-lang", "en");
    globalThis.fetch = async (url) => {
      if (String(url) === "/api/services") return jsonOk(payload);
      throw new Error(`unexpected fetch ${url}`);
    };
  });

  afterEach(() => {
    globalThis.fetch = prevFetch;
    localStorage.removeItem("dnsmarty-lang");
  });

  it("renders accordion headers with route pills and 24h traffic", async () => {
    const { host, unmount } = await mount();
    const cards = host.querySelectorAll("article.svc");
    expect(cards).toHaveLength(2);
    expect(cards[0].querySelector(".sn b").textContent).toBe("Steam");
    expect(cards[0].querySelector(".cnt").textContent).toBe("2 domains");
    expect(cards[0].querySelector(".sroute").textContent).toContain("weighted");
    expect(cards[0].querySelector(".sroute").textContent).toContain("edge-1");
    expect(cards[0].querySelector(".traf").textContent).toContain("12 queries (24h)");
    expect(cards[0].querySelector(".traf").textContent).toContain("2.0 KB");
    expect(cards[0].classList.contains("open")).toBe(false);
    expect(cards[0].querySelector(".svc-head").getAttribute("aria-expanded")).toBe("false");
    expect(cards[1].classList.contains("lampoff")).toBe(true);
    expect(cards[1].querySelector(".sroute").textContent).toContain("no nodes in the route");
    unmount();
  });

  it("expands to the members table and keeps routing out of the body", async () => {
    const { host, unmount } = await mount();
    await act(async () => {
      host.querySelector(".svc-head").click();
    });
    const steam = host.querySelector("article.svc");
    expect(steam.classList.contains("open")).toBe(true);
    expect(steam.querySelector(".svc-head").getAttribute("aria-expanded")).toBe("true");
    const rows = [...steam.querySelectorAll(".dtable tbody tr")].map((tr) => tr.textContent);
    expect(rows[0]).toContain("steam.com");
    expect(rows[0]).toContain("suffix");
    expect(rows[1]).toContain("steampowered.com");
    expect(rows[1]).toContain("exact name");
    expect(steam.querySelector(".dtable tbody tr.dim")).toBeTruthy();
    expect(steam.querySelector(".svc-dom #edit-svc-strategy")).toBeNull();
    expect(steam.querySelector(".svc-dom #new-svc-strategy")).toBeNull();
    expect(steam.querySelector(".dadd textarea")).toBeTruthy();
    unmount();
  });

  it("opens the edit modal from the pencil without expanding the card", async () => {
    const { host, unmount } = await mount();
    await act(async () => {
      host.querySelector('.sacts .abtn[title="Edit"]').click();
    });
    expect(host.querySelector("article.svc.open")).toBeNull();
    // Radix Dialog portals onto document.body, outside the mount host.
    expect(document.querySelector(".modal h2").textContent).toBe("Edit service");
    expect(document.querySelector("#edit-svc-name").value).toBe("Steam");
    expect(document.querySelector("#edit-svc-strategy")).toBeTruthy();
    expect(document.querySelector(".modal .dtable")).toBeNull();
    unmount();
  });
});
