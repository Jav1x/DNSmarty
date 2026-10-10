import { describe, it, expect } from "vitest";
import { createRoot } from "react-dom/client";
import { act } from "react";
import { MemoryRouter, Routes, Route, Navigate } from "react-router-dom";
import { ROUTES } from "./routes.js";

let spyLog = [];

function Spy({ tag }) {
  spyLog.push(tag);
  return null;
}

function Current({ path }) {
  spyLog.push(`@${path}`);
  return null;
}

const appRoutes = [
  <Route key="login" path="/login" element={<Spy tag="login" />} />,
  <Route key="overview" path="/" element={<Spy tag="overview" />} />,
  <Route key="nodes" path="/nodes" element={<Spy tag="nodes" />} />,

  <Route key="access" path={ROUTES.access} element={<Spy tag="access" />} />,
  <Route key="services" path={ROUTES.services} element={<Spy tag="services" />} />,
  <Route key="domains" path="/domains" element={<Navigate to={ROUTES.redirect.domains} replace />} />,

  <Route key="clients" path="/clients" element={<Navigate to={ROUTES.redirect.clients} replace />} />,
  <Route key="logs" path="/logs" element={<Spy tag="logs" />} />,
  <Route key="stats" path="/stats" element={<Spy tag="stats" />} />,
  <Route key="account" path="/account" element={<Spy tag="account" />} />,
  <Route key="settings" path="/settings" element={<Spy tag="settings" />} />,
  <Route key="audit" path="/audit" element={<Spy tag="audit" />} />,
];

function renderRoutesAt(entryPath) {
  spyLog = [];
  const div = document.createElement("div");
  let root;
  act(() => {
    root = createRoot(div);
    root.render(
      <MemoryRouter initialEntries={[entryPath]}>
        <Routes>
          {appRoutes}
          <Route path="*" element={<Spy tag="notfound" />} />
        </Routes>
      </MemoryRouter>,
    );
  });
  const tags = spyLog.filter((x) => !x.startsWith("@"));
  act(() => root.unmount());
  return tags[tags.length - 1] ?? null;
}

describe("Route table of App.jsx (real MemoryRouter run)", () => {
  it("resolves legacy bookmark /clients to /access content — no 404", () => {
    expect(renderRoutesAt("/clients")).toBe("access");
  });

  it("legacy /domains redirects to the Services page", () => {
    expect(renderRoutesAt("/domains")).toBe("services");
  });

  it("/services renders the Services page", () => {
    expect(renderRoutesAt("/services")).toBe("services");
  });

  it("/access renders the Access (ex-Clients) page directly", () => {
    expect(renderRoutesAt("/access")).toBe("access");
  });

  it("/login route exists", () => {
    expect(renderRoutesAt("/login")).toBe("login");
  });

  it("every other path stays as-is (spot check /nodes)", () => {
    expect(renderRoutesAt("/nodes")).toBe("nodes");
  });
});

describe("ROUTES (spec §4 IA)", () => {
  it("defines the new IA paths", () => {
    expect(ROUTES.access).toBe("/access");
    expect(ROUTES.services).toBe("/services");
  });

  it("resolves legacy bookmark /clients to /access — no 404", () => {
    expect(ROUTES.redirect.clients).toBe("/access");
  });

  it("redirects legacy /domains to /services", () => {
    expect(ROUTES.redirect.domains).toBe("/services");
  });
});
