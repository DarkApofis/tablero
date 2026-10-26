import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { meQueryKey } from "@/lib/auth";
import { newQueryClient, stubFetch, withClient } from "@/test/helpers";
import { LoginForm } from "./login-form";

const replace = vi.fn();
vi.mock("next/navigation", () => ({ useRouter: () => ({ replace }) }));

const user = { id: "u1", email: "ana@example.com", name: "Ana López", created_at: "2026-10-01T00:00:00Z" };

function renderForm() {
  const queryClient = newQueryClient();
  render(<LoginForm />, { wrapper: withClient(queryClient) });
  return { queryClient, email: screen.getByLabelText("Correo electrónico"), password: screen.getByLabelText("Contraseña") };
}

const submit = () => userEvent.click(screen.getByRole("button", { name: "Iniciar sesión" }));

describe("LoginForm", () => {
  beforeEach(() => {
    replace.mockReset();
    window.history.replaceState(null, "", "/login");
  });

  it("asks for both fields before sending anything", async () => {
    const calls = stubFetch({});
    renderForm();

    await submit();

    expect(await screen.findByText("Escribe tu correo")).toBeVisible();
    expect(screen.getByText("Escribe tu contraseña")).toBeVisible();
    expect(screen.getByLabelText("Correo electrónico")).toHaveAttribute("aria-invalid", "true");
    expect(calls).toHaveLength(0);
  });

  it("shows the same message for any wrong credentials, and returns focus to the password", async () => {
    const calls = stubFetch({
      "POST /auth/login": () => ({ status: 401, body: { error: { code: "invalid_credentials", message: "Correo o contraseña incorrectos" } } }),
    });
    const { email, password } = renderForm();

    await userEvent.type(email, "ana@example.com");
    await userEvent.type(password, "equivocada");
    await submit();

    expect(await screen.findByRole("alert")).toHaveTextContent("Correo o contraseña incorrectos");
    expect(email).toHaveAttribute("aria-invalid", "true");
    expect(password).toHaveAttribute("aria-invalid", "true");
    await waitFor(() => expect(password).toHaveFocus());
    // A rejected login never tries to renew a session.
    expect(calls.map((c) => c.path)).toEqual(["/auth/login"]);
    expect(replace).not.toHaveBeenCalled();
  });

  it("signs in, caches the user and goes to the page it was sent from", async () => {
    window.history.replaceState(null, "", "/login?next=/boards/b1");
    const calls = stubFetch({
      "POST /auth/login": () => ({ status: 200, body: { user, access_token: "tok", token_type: "Bearer", expires_in: 900 } }),
    });
    const { queryClient, email, password } = renderForm();

    await userEvent.type(email, "  ana@example.com ");
    await userEvent.type(password, "supersecreta");
    await submit();

    await waitFor(() => expect(replace).toHaveBeenCalledWith("/boards/b1"));
    expect(calls[0].body).toEqual({ email: "ana@example.com", password: "supersecreta" });
    expect(queryClient.getQueryData(meQueryKey)).toEqual(user);
    expect(document.cookie).toContain("tablero_session=1");
  });

  it("never follows a next that leaves the site", async () => {
    window.history.replaceState(null, "", "/login?next=//evil.example/");
    stubFetch({ "POST /auth/login": () => ({ status: 200, body: { user, access_token: "tok", token_type: "Bearer", expires_in: 900 } }) });
    const { email, password } = renderForm();

    await userEvent.type(email, "ana@example.com");
    await userEvent.type(password, "supersecreta");
    await submit();

    await waitFor(() => expect(replace).toHaveBeenCalledWith("/"));
  });

  it("explains a connection problem without blaming the credentials", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
    const { email, password } = renderForm();

    await userEvent.type(email, "ana@example.com");
    await userEvent.type(password, "supersecreta");
    await submit();

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("No pudimos iniciar sesión");
    expect(alert).not.toHaveTextContent("Correo o contraseña incorrectos");
    expect(email).not.toHaveAttribute("aria-invalid");
  });
});
