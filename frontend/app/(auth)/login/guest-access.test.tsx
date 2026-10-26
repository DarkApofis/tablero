import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { meQueryKey } from "@/lib/auth";
import { newQueryClient, stubFetch, withClient } from "@/test/helpers";
import { GuestAccess } from "./guest-access";

const replace = vi.fn();
vi.mock("next/navigation", () => ({ useRouter: () => ({ replace }) }));

const guest = { id: "g1", email: "invitado-1@invitados.estudionube.example", name: "Invitado 1A2B", created_at: "2026-10-26T00:00:00Z" };

function renderGuestAccess() {
  const queryClient = newQueryClient();
  const view = render(<GuestAccess />, { wrapper: withClient(queryClient) });
  return { queryClient, ...view };
}

describe("GuestAccess", () => {
  beforeEach(() => replace.mockReset());

  it("shows nothing when the API is not in demo mode", async () => {
    const calls = stubFetch({ "GET /demo": () => ({ status: 200, body: { guest_access: false } }) });
    const { container } = renderGuestAccess();

    await waitFor(() => expect(calls).toHaveLength(1));
    expect(container).toBeEmptyDOMElement();
  });

  it("signs in a guest and opens the demo board", async () => {
    const calls = stubFetch({
      "GET /demo": () => ({ status: 200, body: { guest_access: true } }),
      "POST /auth/guest": () => ({
        status: 201,
        body: { user: guest, access_token: "tok", token_type: "Bearer", expires_in: 900, workspace_id: "w9", board_id: "b9" },
      }),
    });
    const { queryClient } = renderGuestAccess();

    await userEvent.click(await screen.findByRole("button", { name: "Entrar como invitado" }));

    await waitFor(() => expect(replace).toHaveBeenCalledWith("/boards/b9"));
    expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(["GET /demo", "POST /auth/guest"]);
    expect(queryClient.getQueryData(meQueryKey)).toEqual(guest);
    expect(document.cookie).toContain("tablero_session=1");
  });

  it("explains a full demo with the API's message", async () => {
    const message = "Hay muchos invitados ahora mismo. Prueba en un rato o entra con uno de los usuarios de demo.";
    stubFetch({
      "GET /demo": () => ({ status: 200, body: { guest_access: true } }),
      "POST /auth/guest": () => ({ status: 429, body: { error: { code: "guest_limit", message } } }),
    });
    renderGuestAccess();

    await userEvent.click(await screen.findByRole("button", { name: "Entrar como invitado" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(screen.getByRole("button", { name: "Entrar como invitado" })).toBeEnabled();
    expect(replace).not.toHaveBeenCalled();
  });
});
