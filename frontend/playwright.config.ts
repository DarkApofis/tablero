import { defineConfig, devices } from "@playwright/test";

// End-to-end tests against the real app (docker compose up).
// E2E_API_URL and E2E_PORT move them off the defaults if those ports are taken;
// the API's CORS_ORIGINS must then name the frontend's origin.
const API_URL = process.env.E2E_API_URL ?? "http://localhost:8080";
const PORT = Number(process.env.E2E_PORT ?? 3000);

export default defineConfig({
  testDir: "./e2e",
  // The tests share one API and its users; each creates its own board.
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [["github"], ["html", { open: "never" }]] : "list",
  globalSetup: "./e2e/global-setup.ts",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    ...devices["Desktop Chrome"],
    baseURL: `http://localhost:${PORT}`,
    locale: "es-ES",
    trace: "retain-on-failure",
  },
  webServer: {
    // Only when nothing answers on PORT: with the compose stack up (and in CI),
    // the tests run against its web container.
    command: `pnpm build && pnpm start -p ${PORT}`,
    url: `http://localhost:${PORT}/login`,
    reuseExistingServer: true,
    timeout: 240_000,
    env: { NEXT_PUBLIC_API_URL: API_URL },
  },
});
