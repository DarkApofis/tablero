import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { EmailChips, isEmail, splitEmails } from "@/components/ui/email-chips";
import { Segmented } from "@/components/ui/segmented";
import { columnMoves } from "./column-order";
import { slugify, slugProblem } from "./slug";

describe("addresses", () => {
  it("come out of names like the API makes them", () => {
    expect(slugify("Diseño de Producto")).toBe("diseno-de-producto");
    expect(slugify("  Equipo · Ñandú!! ")).toBe("equipo-nandu");
    expect(slugify("Q4")).toBe("ws-q4");
    expect(slugify("¿?")).toBe("");
    expect(slugProblem(slugify("x ".repeat(60)))).toBeNull();
  });

  it("are checked like the API checks them", () => {
    expect(slugProblem("equipo-2")).toBeNull();
    expect(slugProblem("eq")).toMatch(/Entre 3 y 40/);
    for (const bad of ["-equipo", "equipo-", "equi--po", "diseño", "Mayús"]) expect(slugProblem(bad)).not.toBeNull();
  });
});

describe("column moves", () => {
  const apply = (current: string[], moves: { id: string; after: string | null }[]) => {
    const order = [...current];
    for (const m of moves) {
      order.splice(order.indexOf(m.id), 1);
      order.splice(m.after === null ? 0 : order.indexOf(m.after) + 1, 0, m.id);
    }
    return order;
  };

  it("are none when nothing changed", () => {
    expect(columnMoves(["a", "b", "c"], ["a", "b", "c"])).toEqual([]);
  });

  it("reach the dragged order", () => {
    for (const desired of [["c", "a", "b"], ["b", "c", "a"], ["a", "c", "b"], ["d", "c", "b", "a"]]) {
      const current = [...desired].sort();
      const moves = columnMoves(current, desired);
      expect(apply(current, moves)).toEqual(desired);
      expect(moves.length).toBeLessThan(desired.length);
    }
  });

  it("move one column dragged to the start with one request", () => {
    expect(columnMoves(["a", "b", "c", "d"], ["d", "a", "b", "c"])).toEqual([{ id: "d", after: null }]);
  });
});

describe("EmailChips", () => {
  function Field() {
    const [emails, setEmails] = useState<string[]>([]);
    return <EmailChips label="Invitar personas" value={emails} onChange={setEmails} />;
  }

  it("turns what is typed into chips, and flags an invalid one", async () => {
    render(<Field />);
    const field = screen.getByLabelText("Invitar personas");
    await userEvent.type(field, "Ana@X.com,julia@ ");
    expect(screen.getByText("ana@x.com")).toBeVisible();
    expect(screen.getByText("«julia@» no es un correo válido. Corrígelo o quítalo.")).toBeVisible();

    await userEvent.click(screen.getByRole("button", { name: "Quitar julia@" }));
    expect(screen.queryByText(/no es un correo válido/)).toBeNull();
    await userEvent.type(field, "{Backspace}");
    expect(screen.queryByText("ana@x.com")).toBeNull();
  });

  it("splits pasted lists", () => {
    expect(splitEmails("a@x.com; B@x.com\nc@x.com")).toEqual(["a@x.com", "b@x.com", "c@x.com"]);
    expect(isEmail("a@x.com")).toBe(true);
    expect(isEmail("a@x")).toBe(false);
  });
});

describe("Segmented", () => {
  it("is a radio group the arrow keys move through", async () => {
    function Roles() {
      const [role, setRole] = useState("member");
      const options = [
        { value: "member", label: "Miembro" },
        { value: "viewer", label: "Solo lectura" },
        { value: "admin", label: "Admin" },
      ];
      return <Segmented label="Rol de los invitados" options={options} value={role} onChange={setRole} />;
    }
    render(<Roles />);
    const group = screen.getByRole("radiogroup", { name: "Rol de los invitados" });
    expect(group).toBeVisible();
    expect(screen.getByRole("radio", { name: "Miembro" })).toBeChecked();

    screen.getByRole("radio", { name: "Miembro" }).focus();
    await userEvent.keyboard("{ArrowLeft}");
    expect(screen.getByRole("radio", { name: "Admin" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "Admin" })).toHaveFocus();
  });
});
