import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, type Browser, type Page } from "@playwright/test";

// Same defaults as playwright.config.ts.
export const API_URL = process.env.E2E_API_URL ?? "http://localhost:8080";
const BASE_URL = `http://localhost:${process.env.E2E_PORT ?? 3000}`;
export const STATE_FILE = join(__dirname, ".state", "seed.json");

export type SeedUser = { id: string; name: string; email: string; password: string };
export type Seed = { workspaceId: string; ana: SeedUser; bruno: SeedUser; vera: SeedUser };

/** What global-setup.ts created: three users in one workspace (admin, member, viewer). */
export function seed(): Seed {
  return JSON.parse(readFileSync(STATE_FILE, "utf8")) as Seed;
}

/** A call to the API as `token`, failing the test on anything but a 2xx. */
export async function apiCall<T>(method: string, path: string, token: string | null, body?: unknown): Promise<T> {
  const res = await fetch(new URL(path, API_URL), {
    method,
    headers: { "Content-Type": "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${method} ${path} → ${res.status} ${await res.text()}`);
  return (res.status === 204 ? undefined : await res.json()) as T;
}

export async function tokenFor(user: SeedUser) {
  const session = await apiCall<{ access_token: string }>("POST", "/auth/login", null, {
    email: user.email,
    password: user.password,
  });
  return session.access_token;
}

/**
 * A board of its own for one test: "Por hacer" with `titles` as tasks, and an
 * empty "Hecho". Created by the workspace's admin.
 */
export async function createBoard(name: string, titles: string[]) {
  const token = await tokenFor(seed().ana);
  const board = await apiCall<{ id: string }>("POST", `/workspaces/${seed().workspaceId}/boards`, token, {
    name: `${name} ${Date.now()}`,
  });
  const todo = await apiCall<{ id: string }>("POST", `/boards/${board.id}/columns`, token, { name: "Por hacer" });
  const done = await apiCall<{ id: string }>("POST", `/boards/${board.id}/columns`, token, { name: "Hecho" });
  const tasks: { id: string; title: string }[] = [];
  for (const title of titles) {
    tasks.push(await apiCall<{ id: string; title: string }>("POST", `/columns/${todo.id}/tasks`, token, { title }));
  }
  return { id: board.id, todoId: todo.id, doneId: done.id, tasks };
}

/**
 * Opens `path` as `user` in a browser context of its own (its own cookies, as a
 * second person on another computer would have). Signs in through the API, which
 * leaves the session cookies in the context, plus the app's session flag.
 */
export async function openAs(browser: Browser, user: SeedUser, path: string): Promise<Page> {
  const context = await browser.newContext();
  const res = await context.request.post(`${API_URL}/auth/login`, { data: { email: user.email, password: user.password } });
  expect(res.ok(), `login as ${user.email}`).toBe(true);
  await context.addCookies([{ name: "tablero_session", value: "1", url: BASE_URL }]);
  const page = await context.newPage();
  await page.goto(new URL(path, BASE_URL).toString());
  return page;
}

/** The board's live stream is open, so what others do from now on shows up here. */
export async function expectLive(page: Page) {
  await expect(page.getByRole("status").filter({ hasText: "En vivo" })).toBeVisible();
}

/** A column of the board, by its name. */
export const column = (page: Page, name: string) => page.getByRole("region", { name, exact: true });
