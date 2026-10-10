import { describe, expect, it } from "vitest";
import { moveId } from "./nodes";

describe("moveId", () => {
  it("moves an id to another slot", () => {
    expect(moveId(["a", "b", "c"], "a", "c")).toEqual(["b", "c", "a"]);
    expect(moveId(["a", "b", "c"], "c", "a")).toEqual(["c", "a", "b"]);
  });

  it("returns the same array when the drop is a no-op", () => {
    const ids = ["a", "b"];
    expect(moveId(ids, "a", "a")).toBe(ids);
    expect(moveId(ids, "x", "a")).toBe(ids);
  });
});
