import { expect, test } from "@playwright/test";
import { apiCall, column, createBoard, expectLive, openAs, seed, tokenFor, type SeedUser } from "./support";

const PASSWORD = "e2e-contraseña-segura";

async function register(name: string): Promise<SeedUser & { token: string }> {
  const email = `e2e-${name.toLowerCase()}-${Date.now().toString(36)}@example.com`;
  const s = await apiCall<{ user: { id: string }; access_token: string }>("POST", "/auth/register", null, { name, email, password: PASSWORD });
  return { id: s.user.id, name, email, password: PASSWORD, token: s.access_token };
}

test("un admin invita por correo y con un enlace, y quien lo abre se une", async ({ browser }) => {
  const admin = await register("Dueña");
  const ws = await apiCall<{ id: string; name: string }>("POST", "/workspaces", admin.token, { name: `Estudio ${Date.now()}` });
  const page = await openAs(browser, admin, `/workspaces/${ws.id}`);

  await page.getByRole("button", { name: "Invitar" }).click();
  const dialog = page.getByRole("dialog", { name: `Invitar a ${ws.name}` });
  const pending = `pendiente-${Date.now()}@example.com`;
  await dialog.getByLabel("Correos").fill(`${seed().bruno.email}, ${pending}`);
  await dialog.getByRole("button", { name: "Enviar" }).click();
  await expect(dialog.getByText("Invitación hecha")).toBeVisible();
  await expect(dialog.getByText(/Bruno ya es miembro/)).toBeVisible();
  await expect(dialog.getByRole("region", { name: "Pendientes" })).toContainText(pending);

  await dialog.getByRole("button", { name: "Crear enlace" }).click();
  const code = dialog.locator("code");
  await expect(code).toContainText("/join/");
  const token = (await code.textContent())!.split("/join/")[1].trim();
  await dialog.getByRole("button", { name: "Listo" }).click();
  await expect(page.getByRole("link", { name: /^Miembros/ })).toHaveText("Miembros 2");

  // Someone with the link joins as Miembro.
  const guest = await register("Enlace");
  const joiner = await openAs(browser, guest, `/join/${token}`);
  await expect(joiner.getByRole("heading", { name: `Te invitaron a ${ws.name}` })).toBeVisible();
  await expect(joiner.getByText("Entrarás como Miembro.")).toBeVisible();
  await joiner.getByRole("button", { name: `Unirme a ${ws.name}` }).click();
  await expect(joiner.getByRole("heading", { name: ws.name })).toBeVisible();
  await expect(joiner.getByText("Miembro ·")).toBeVisible();
  // Not an admin: no settings tab, no invite or new board.
  await expect(joiner.getByRole("link", { name: "Ajustes" })).toHaveCount(0);
  await expect(joiner.getByRole("button", { name: "Invitar" })).toHaveCount(0);
});

test("un admin renombra, duplica, archiva y restaura un tablero, y cambia los ajustes", async ({ browser }) => {
  const board = await createBoard("Gestión", ["Una tarea"]);
  const name = (await apiCall<{ name: string }>("GET", `/boards/${board.id}`, await tokenFor(seed().ana))).name;
  const page = await openAs(browser, seed().ana, `/workspaces/${seed().workspaceId}`);
  const card = (n: string) => page.getByRole("article").filter({ has: page.getByRole("link", { name: n, exact: true }) });

  await card(name).getByRole("button", { name: `Acciones del tablero ${name}` }).click();
  await page.getByRole("menuitem", { name: "Renombrar" }).click();
  await page.getByLabel("Nuevo nombre del tablero").fill(`${name} bis`);
  await page.getByRole("button", { name: "Guardar nombre" }).click();
  await expect(card(`${name} bis`)).toBeVisible();
  await expect(card(`${name} bis`)).toContainText("1 tarea");

  await card(`${name} bis`).getByRole("button", { name: /Acciones del tablero/ }).click();
  await page.getByRole("menuitem", { name: "Duplicar" }).click();
  await expect(page.getByText(`Se creó «${name} bis (copia)».`)).toBeVisible();
  await expect(card(`${name} bis (copia)`)).toContainText("1 tarea");

  await card(`${name} bis`).getByRole("button", { name: /Acciones del tablero/ }).click();
  await page.getByRole("menuitem", { name: "Archivar" }).click();
  await expect(page.getByText(`«${name} bis» se archivó.`, { exact: false })).toBeVisible();
  await expect(card(`${name} bis`)).toHaveCount(0);

  await page.getByRole("link", { name: "Archivados" }).click();
  await page.getByRole("button", { name: `Restaurar: ${name} bis` }).click();
  await expect(page.getByText(`«${name} bis» se restauró.`)).toBeVisible();
  await page.getByRole("link", { name: /^Tableros/ }).click();
  await expect(card(`${name} bis`)).toBeVisible();

  await page.getByRole("link", { name: "Ajustes" }).click();
  await page.getByLabel("Descripción").fill("Equipo de pruebas de punta a punta");
  await expect(page.getByText("● Cambios sin guardar")).toBeVisible();
  await page.getByRole("button", { name: "Guardar cambios" }).click();
  await expect(page.getByText("✓ Guardado")).toBeVisible();
  await expect(page.getByText("Equipo de pruebas de punta a punta").first()).toBeVisible();
});

test("un tablero privado solo lo ven los admins y quien lo tiene; uno archivado no se cambia", async ({ browser }) => {
  const board = await createBoard("Privado", ["Secreta"]);
  const ana = await openAs(browser, seed().ana, `/boards/${board.id}`);
  await expectLive(ana);

  // Ana makes it private, renames a column and adds one, in the settings.
  await ana.getByRole("button", { name: "Ajustes del tablero" }).click();
  const settings = ana.getByRole("dialog", { name: "Ajustes del tablero" });
  await settings.getByRole("radio", { name: /Solo personas invitadas/ }).check();
  await settings.getByRole("button", { name: "Renombrar columna Hecho" }).click();
  await settings.getByLabel("Nombre de la columna Hecho").fill("Terminado");
  await settings.getByRole("button", { name: "+ Añadir columna" }).click();
  await settings.getByLabel("Nombre de la columna nueva").fill("Revisión");
  await settings.getByRole("button", { name: "Guardar" }).click();
  await expect(settings).toBeHidden();
  await expect(column(ana, "Terminado")).toBeVisible();
  await expect(column(ana, "Revisión")).toBeVisible();
  await expect(ana.getByText("Privado", { exact: true })).toBeVisible();

  const bruno = await openAs(browser, seed().bruno, `/boards/${board.id}`);
  await expect(bruno.getByRole("heading", { name: "No tienes acceso a este tablero" })).toBeVisible();

  // Given to Bruno, he sees it.
  await ana.getByRole("button", { name: "Ajustes del tablero" }).click();
  await settings.getByRole("checkbox", { name: /Bruno/ }).check();
  await settings.getByRole("button", { name: "Guardar" }).click();
  await expect(settings).toBeHidden();
  await bruno.reload();
  await expect(column(bruno, "Por hacer").getByText("Secreta")).toBeVisible();

  // Archived: everyone reads it, nobody changes it until it is restored.
  await ana.getByRole("button", { name: "Ajustes del tablero" }).click();
  await settings.getByRole("button", { name: "Archivar tablero" }).click();
  await expect(ana.getByText("Este tablero está archivado")).toBeVisible();
  await bruno.reload();
  await expect(bruno.getByText("Este tablero está archivado")).toBeVisible();
  await expect(bruno.getByRole("button", { name: /Añadir tarea/ })).toHaveCount(0);
  await ana.getByRole("button", { name: "Restaurar" }).click();
  await expect(ana.getByText("Este tablero está archivado")).toBeHidden();
  await expect(column(ana, "Por hacer").getByRole("button", { name: "Añadir tarea en Por hacer" })).toBeVisible();
});
