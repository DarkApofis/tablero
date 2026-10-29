import { expect, test } from "@playwright/test";
import { apiCall, column, createBoard, expectLive, openAs, seed, type SeedUser } from "./support";

// A fresh account of its own, so its workspace quota starts at 0 on every run.
async function freshUser(): Promise<SeedUser> {
  const email = `e2e-crea-${Date.now().toString(36)}@example.com`;
  const password = "e2e-contraseña-segura";
  const s = await apiCall<{ user: { id: string } }>("POST", "/auth/register", null, { name: "Creadora", email, password });
  return { id: s.user.id, name: "Creadora", email, password };
}

test("una cuenta crea su workspace, un tablero, una columna y una tarea, hasta el límite de workspaces", async ({ browser }) => {
  const page = await openAs(browser, await freshUser(), "/");

  await expect(page.getByText("Puedes crear 2 más")).toBeVisible();
  await expect(page.getByRole("button", { name: "Crear workspace nuevo" })).toBeVisible();
  await page.getByRole("button", { name: "Crear workspace", exact: true }).click();
  await page.getByLabel("Nombre del workspace").fill("Mi equipo");
  await page.getByRole("button", { name: "Crear", exact: true }).click();

  // Its admin: it may create boards, which start with three columns.
  await expect(page.getByRole("heading", { name: "Mi equipo" })).toBeVisible();
  await expect(page.getByText("Admin ·")).toBeVisible();
  await page.getByRole("button", { name: "Nuevo tablero" }).click();
  await page.getByLabel("Nombre del tablero").fill("Sprint");
  await page.getByRole("button", { name: "Crear tablero" }).click();

  await expect(page.getByRole("heading", { name: "Sprint" })).toBeVisible();
  await expectLive(page);
  for (const name of ["Pendiente", "En progreso", "Hecho"]) await expect(column(page, name)).toBeVisible();

  await page.getByRole("button", { name: "Añadir columna" }).click();
  await page.getByLabel("Nombre de la nueva columna").fill("Revisión");
  await page.getByRole("button", { name: "Añadir columna" }).click();
  await expect(column(page, "Revisión")).toBeVisible();

  await column(page, "Pendiente").getByRole("button", { name: "Añadir tarea en Pendiente" }).click();
  await page.getByLabel("Título de la nueva tarea en Pendiente").fill("Primera tarea");
  await page.keyboard.press("Enter");
  await expect(column(page, "Pendiente").getByText("Primera tarea")).toBeVisible();

  await page.reload();
  await expect(column(page, "Pendiente").getByText("Primera tarea")).toBeVisible();
  await expect(column(page, "Revisión")).toBeVisible();

  // A second workspace is the last one.
  await page.goto("/");
  await page.getByRole("button", { name: "Crear workspace nuevo" }).click();
  await page.getByLabel("Nombre del workspace").fill("Otro equipo");
  await page.getByRole("button", { name: "Crear", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Otro equipo" })).toBeVisible();
  await page.goto("/");
  await expect(page.getByText("Has creado 2 de 2 workspaces, el máximo por persona.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Crear workspace", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Crear workspace nuevo" })).toHaveCount(0);
});

test("un miembro crea tareas pero no columnas, y un viewer ninguna de las dos", async ({ browser }) => {
  const board = await createBoard("Permisos", ["Existente"]);

  const bruno = await openAs(browser, seed().bruno, `/boards/${board.id}`);
  await expectLive(bruno);
  await column(bruno, "Por hacer").getByRole("button", { name: "Añadir tarea en Por hacer" }).click();
  await bruno.getByLabel("Título de la nueva tarea en Por hacer").fill("De Bruno");
  await bruno.keyboard.press("Enter");
  await expect(column(bruno, "Por hacer").getByText("De Bruno")).toBeVisible();
  await expect(bruno.getByRole("button", { name: "Añadir columna" })).toHaveCount(0);

  const vera = await openAs(browser, seed().vera, `/boards/${board.id}`);
  await expectLive(vera);
  // Vera sees Bruno's task (live or on load) but can add nothing.
  await expect(column(vera, "Por hacer").getByText("De Bruno")).toBeVisible();
  await expect(vera.getByRole("button", { name: /Añadir tarea/ })).toHaveCount(0);
  await expect(vera.getByRole("button", { name: "Añadir columna" })).toHaveCount(0);
});
