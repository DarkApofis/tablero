import { expect, test } from "@playwright/test";
import { API_URL, column, createBoard, expectLive, openAs, seed } from "./support";

// The Content-Security-Policy must not break the app: every page loads, goes
// live over the API, and the browser reports no violation.
test("las cabeceras de seguridad están y la CSP no bloquea nada de la app", async ({ browser }) => {
  const board = await createBoard("Seguridad", ["Una tarea"]);
  const context = await browser.newContext();
  await context.addInitScript(() => {
    const seen: string[] = [];
    Object.assign(window, { __cspViolations: seen });
    document.addEventListener("securitypolicyviolation", (e) => seen.push(`${e.violatedDirective} ${e.blockedURI}`));
  });

  const page = await context.newPage();
  const res = await page.goto("/login");
  const headers = res!.headers();
  expect(headers["content-security-policy"]).toContain("frame-ancestors 'none'");
  // 'self', plus the API's origin unless the frontend proxies it under /api.
  expect(headers["content-security-policy"]).toMatch(/connect-src 'self'( https?:\/\/\S+)?;/);
  expect(headers["x-content-type-options"]).toBe("nosniff");
  expect(headers["referrer-policy"]).toBe("strict-origin-when-cross-origin");
  expect(headers["x-powered-by"]).toBeUndefined();
  await expect(page.getByRole("button", { name: "Iniciar sesión" })).toBeEnabled();
  expect(await page.evaluate(() => (window as { __cspViolations?: string[] }).__cspViolations)).toEqual([]);

  // A signed-in board, live: Chrome logs every CSP violation to the console.
  const ana = await openAs(browser, seed().ana, `/boards/${board.id}`);
  await expectLive(ana);
  await expect(column(ana, "Por hacer").getByText("Una tarea")).toBeVisible();
  const violations: string[] = [];
  ana.on("console", (m) => void (m.text().includes("Content Security Policy") && violations.push(m.text())));
  await ana.reload();
  await expectLive(ana);
  expect(violations).toEqual([]);

  // The API refuses a state-changing request from another origin.
  const csrf = await fetch(new URL("/auth/logout", API_URL), { method: "POST", headers: { Origin: "https://evil.example" } });
  expect(csrf.status).toBe(403);
});
