import { mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { Client } from "pg";
import { API_URL, apiCall, STATE_FILE, type Seed, type SeedUser } from "./support";

// The database of the compose stack; its port is published on the host.
const DATABASE_URL = process.env.E2E_DATABASE_URL ?? "postgres://tablero:tablero@localhost:5432/tablero?sslmode=disable";
const PASSWORD = "e2e-contraseña-segura";

/**
 * Three people in one workspace, fresh for each run: Ana (admin), Bruno (member)
 * and Vera (viewer). The API has no invitations yet, so the two memberships go
 * straight into the database.
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

  const db = new Client({ connectionString: DATABASE_URL });
  await db.connect();
  try {
    await db.query(
      `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'member'), ($1, $3, 'viewer')`,
      [workspace.id, bruno.id, vera.id],
    );
  } finally {
    await db.end();
  }

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
      throw new Error(`The API at ${API_URL} is not answering. Start it with: docker compose --profile app up -d --build --wait`);
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
}
