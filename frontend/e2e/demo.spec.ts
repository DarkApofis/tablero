import { expect, test } from "@playwright/test";
import { API_URL, column, expectLive } from "./support";

// Needs the API in demo mode (DEMO_MODE=true, as in CI); skipped otherwise.
test("un invitado entra a la demo desde el login y ve el tablero de Estudio Nube", async ({ browser }) => {
  const demo = (await (await fetch(new URL("/demo", API_URL))).json()) as { guest_access: boolean };
  test.skip(!demo.guest_access, "the API is not in demo mode");

  const page = await (await browser.newContext()).newPage();
  await page.goto("/login");
  await page.getByRole("button", { name: "Entrar como invitado" }).click();

  await expect(page).toHaveURL(/\/boards\/[0-9a-f-]{36}$/);
  await expect(page.getByRole("heading", { name: "Rediseño web · Panadería Trigo" })).toBeVisible();
  await expectLive(page);
  for (const name of ["Pendiente", "En progreso", "En revisión", "Hecho"]) {
    await expect(column(page, name)).toBeVisible();
  }
  await expect(column(page, "En revisión").getByText("Aprobar paleta de color y tipografías")).toBeVisible();
  // The history is there too.
  await page.getByRole("button", { name: "Actividad" }).click();
  await expect(page.getByRole("complementary", { name: "Actividad del tablero" })).toContainText("Bruno Díaz");
});
