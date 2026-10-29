import { mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { API_URL, apiCall, STATE_FILE, type Seed, type SeedUser } from "./support";

const PASSWORD = "e2e-contraseña-segura";

/**
 * Three people in one workspace, fresh for each run: Ana (admin), Bruno (member)
 * and Vera (viewer). Ana creates it and invites the other two by email: they
 * have an account already, so they join at once.
 */
export default async function globalSetup() {
  await waitForApi();
  const run = Date.now().toString(36);

  const register = async (name: string): Promise<SeedUser> => {
    const email = `e2e-${name.toLowerCase()}-${run}@example.com`;
    const session = await apiCall<{ user: { id: string } }>("POST", "/auth/register", null, { name, email, password: PASSWORD });
    return { id: session.user.id, name, email, password: PASSWORD };
  };
  const ana = await register("Ana");
  const bruno = await register("Bruno");
  const vera = await register("Vera");

  const { access_token } = await apiCall<{ access_token: string }>("POST", "/auth/login", null, {
    email: ana.email,
    password: PASSWORD,
  });
  const workspace = await apiCall<{ id: string }>("POST", "/workspaces", access_token, { name: `E2E ${run}` });
  const invite = (email: string, role: string) =>
    apiCall("POST", `/workspaces/${workspace.id}/invitations`, access_token, { emails: [email], role });
  await invite(bruno.email, "member");
  await invite(vera.email, "viewer");

  const seed: Seed = { workspaceId: workspace.id, ana, bruno, vera };
  mkdirSync(dirname(STATE_FILE), { recursive: true });
  writeFileSync(STATE_FILE, JSON.stringify(seed, null, 2));
}

async function waitForApi() {
  const deadline = Date.now() + 60_000;
  for (;;) {
    try {
      const res = await fetch(new URL("/health", API_URL));
      if (res.ok) return;
    } catch {
      // not up yet
    }
    if (Date.now() > deadline) {
      throw new Error(`The API at ${API_URL} is not answering. Start it with: docker compose up -d --build --wait`);
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
}
