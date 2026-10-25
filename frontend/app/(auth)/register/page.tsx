import type { Metadata } from "next";
import { RegisterForm } from "./register-form";

export const metadata: Metadata = { title: "Crear cuenta · Tablero" };

export default function RegisterPage() {
  return (
    <>
      <div>
        <h1 className="text-2xl font-semibold tracking-[-.02em]">Crea tu cuenta</h1>
        <p className="mt-1.5 text-sm text-muted">Organiza el trabajo de tu equipo en tiempo real.</p>
      </div>
      <RegisterForm />
    </>
  );
}
