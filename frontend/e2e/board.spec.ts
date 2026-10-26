import { expect, test } from "@playwright/test";
import { API_URL, column, createBoard, expectLive, openAs, seed, tokenFor } from "./support";

test("una persona mueve una tarjeta y la otra la ve moverse sin recargar", async ({ browser }) => {
  const board = await createBoard("Mover", ["Tarjeta que se mueve", "Otra tarjeta"]);
  const card = board.tasks[0];
  const ana = await openAs(browser, seed().ana, `/boards/${board.id}`);
  const bruno = await openAs(browser, seed().bruno, `/boards/${board.id}`);
  await expectLive(ana);
  await expectLive(bruno);
  await expect(column(bruno, "Por hacer").getByText(card.title)).toBeVisible();
  // Anything that reloads Bruno's page would drop this.
  await bruno.evaluate(() => Object.assign(window, { __sinRecargar: true }));

  // Ana moves it with the keyboard: Space picks it up, → to "Hecho", Space drops it.
  await ana.locator(`[data-task-id="${card.id}"]`).focus();
  await ana.keyboard.press("Space");
  await ana.keyboard.press("ArrowRight");
  await ana.keyboard.press("Space");
  await expect(column(ana, "Hecho").getByText(card.title)).toBeVisible();

  await expect(column(bruno, "Hecho").getByText(card.title)).toBeVisible();
  await expect(column(bruno, "Por hacer").getByText(card.title)).toHaveCount(0);
  expect(await bruno.evaluate(() => (window as { __sinRecargar?: boolean }).__sinRecargar)).toBe(true);

  // The activity of the other person shows up live too.
  await bruno.getByRole("button", { name: "Actividad" }).click();
  await expect(bruno.getByRole("complementary", { name: "Actividad del tablero" })).toContainText(
    `movió «${card.title}» a Hecho`,
  );
});

test("dos personas editan la misma tarea a la vez y la segunda ve el aviso de conflicto", async ({ browser }) => {
  const board = await createBoard("Conflicto", ["Tarea editada a la vez"]);
  const task = board.tasks[0];
  const path = `/boards/${board.id}?task=${task.id}`;
  const ana = await openAs(browser, seed().ana, path);
  const bruno = await openAs(browser, seed().bruno, path);
  await expectLive(ana);
  await expectLive(bruno);

  // Bruno's save leaves while Ana's is still to come, but only reaches the API
  // after Ana's has landed: the same race as two people pressing keys at once,
  // without depending on timing.
  let letBrunoThrough!: () => void;
  const anaSaved = new Promise<void>((resolve) => (letBrunoThrough = resolve));
  await bruno.route(`${API_URL}/tasks/${task.id}`, async (route) => {
    if (route.request().method() === "PATCH") await anaSaved;
    await route.continue();
  });
  const brunoSaving = bruno.waitForRequest((r) => r.method() === "PATCH" && r.url().endsWith(`/tasks/${task.id}`));
  await bruno.getByLabel("Descripción").fill("Versión de Bruno");
  await brunoSaving;

  await ana.getByLabel("Descripción").fill("Versión de Ana");
  await expect(ana.getByText("✓ Guardado")).toBeVisible();
  letBrunoThrough();

  const conflict = bruno.getByRole("alert").filter({ hasText: "La tarea cambió mientras editabas" });
  await expect(conflict).toBeVisible();
  await expect(bruno.getByText("+ Versión de Ana")).toBeVisible();
  await expect(bruno.getByLabel("Descripción")).toHaveValue("Versión de Bruno");

  // Bruno keeps his text, saved on top of Ana's version; Ana sees it arrive.
  await conflict.getByRole("button", { name: "Reaplicar mis cambios" }).click();
  await expect(conflict).toHaveCount(0);
  await expect(bruno.getByText("✓ Guardado")).toBeVisible();
  await expect(ana.getByLabel("Descripción")).toHaveValue("Versión de Bruno");
});

test("una persona con rol de solo lectura no puede arrastrar tarjetas", async ({ browser }) => {
  const board = await createBoard("Solo lectura", ["No se puede mover"]);
  const card = board.tasks[0];
  const vera = await openAs(browser, seed().vera, `/boards/${board.id}`);
  await expectLive(vera);
  await expect(vera.getByText("Solo lectura", { exact: true })).toBeVisible();

  // Nothing on the board is draggable…
  await expect(vera.locator('[aria-roledescription="tarea arrastrable"]')).toHaveCount(0);
  const moves: string[] = [];
  vera.on("request", (r) => r.url().includes("/move") && moves.push(r.url()));

  // …so a drag with the mouse leaves the card where it was and asks nothing of the API.
  const target = column(vera, "Hecho");
  await vera.locator(`[data-task-id="${card.id}"]`).dragTo(target);
  await expect(column(vera, "Por hacer").getByText(card.title)).toBeVisible();
  await expect(target.getByText(card.title)).toHaveCount(0);
  expect(moves).toEqual([]);

  // Space opens the task (read-only) instead of picking the card up.
  await vera.keyboard.press("Escape");
  await vera.locator(`[data-task-id="${card.id}"]`).focus();
  await vera.keyboard.press("Space");
  const panel = vera.getByRole("complementary", { name: `Tarea: ${card.title}` });
  await expect(panel).toContainText("Solo lectura");
  await expect(panel.getByRole("textbox")).toHaveCount(0);

  // And the API, which is what actually decides, refuses the move anyway.
  const res = await fetch(new URL(`/tasks/${card.id}/move`, API_URL), {
    method: "PATCH",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${await tokenFor(seed().vera)}` },
    body: JSON.stringify({ columnId: board.doneId, version: 1 }),
  });
  expect(res.status).toBe(403);

  await vera.reload();
  await expect(column(vera, "Por hacer").getByText(card.title)).toBeVisible();
});
