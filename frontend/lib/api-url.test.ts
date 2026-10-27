import { describe, expect, it } from "vitest";
import { apiUrl } from "./api";

describe("apiUrl", () => {
  it("joins paths to an API on another origin", () => {
    expect(apiUrl("/auth/refresh", "http://localhost:8080").toString()).toBe("http://localhost:8080/auth/refresh");
    expect(apiUrl("/boards/b1/events", "https://api.example.com/").toString()).toBe("https://api.example.com/boards/b1/events");
  });

  it("keeps the prefix of an API served by this site", () => {
    // new URL("/auth/refresh", ".../api") would drop it.
    expect(apiUrl("/auth/refresh", "/api").toString()).toBe(`${window.location.origin}/api/auth/refresh`);
    expect(apiUrl("/boards/b1/events", "https://tablero.vercel.app/api").pathname).toBe("/api/boards/b1/events");
  });
});
