import { describe, it, expect } from "vitest";
import { ROUTES } from "./routes.js";

describe("ROUTES (spec §4 IA)", () => {
  it("defines the new IA paths", () => {
    expect(ROUTES.access).toBe("/access");
    expect(ROUTES.services).toBe("/services");
  });

  it("resolves legacy bookmark /clients to /access — no 404", () => {
    expect(ROUTES.redirect.clients).toBe("/access");
  });

  it("keeps legacy /domains working", () => {
    expect(ROUTES.redirect.domains).toBe("/domains");
  });

  it("every redirect target is a real app path (starts with /)", () => {
    for (const target of Object.values(ROUTES.redirect)) {
      expect(target).toMatch(/^\//);
      expect(target).not.toBe("");
    }
  });
});
