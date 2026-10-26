import type { Metadata } from "next";
import { GuestAccess } from "./guest-access";
import { LoginForm } from "./login-form";

export const metadata: Metadata = { title: "Iniciar sesión · Tablero" };

export default function LoginPage() {
  return (
    <>
      <div>
        <h1 className="text-2xl font-semibold tracking-[-.02em]">Hola de nuevo</h1>
        <p className="mt-1.5 text-sm text-muted">Entra para ver tus tableros en vivo.</p>
      </div>
      <LoginForm />
      <GuestAccess />
    </>
  );
}
