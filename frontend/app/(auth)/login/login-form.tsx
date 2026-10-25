"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { useQueryClient } from "@tanstack/react-query";
import { z } from "zod";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Notice } from "@/components/ui/notice";
import { ApiError } from "@/lib/api";
import { login, meQueryKey } from "@/lib/auth";
import { describeError, nextPath } from "../form-errors";
import { useFocusAfterSubmit } from "../use-focus-after-submit";

// Only presence is checked here: format and length are the API's call, and any
// mismatch must look the same as a wrong password.
const schema = z.object({
  email: z.string().trim().min(1, "Escribe tu correo"),
  password: z.string().min(1, "Escribe tu contraseña"),
});

type Values = z.infer<typeof schema>;

const INVALID_CREDENTIALS = "Correo o contraseña incorrectos";

export function LoginForm() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [navigating, startNavigation] = useTransition();
  const [failure, setFailure] = useState<{ title: string; text?: string } | null>(null);
  const {
    register,
    handleSubmit,
    setFocus,
    formState: { errors, isSubmitting },
  } = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { email: "", password: "" } });
  const focusLater = useFocusAfterSubmit(isSubmitting, setFocus);

  const onSubmit = async (values: Values) => {
    setFailure(null);
    try {
      const user = await login(values);
      queryClient.setQueryData(meQueryKey, user);
      startNavigation(() => router.replace(nextPath()));
    } catch (err) {
      focusLater("password");
      setFailure(
        err instanceof ApiError && err.status === 401
          ? { title: INVALID_CREDENTIALS, text: "Revisa los datos e inténtalo de nuevo." }
          : { title: "No pudimos iniciar sesión", text: describeError(err) },
      );
    }
  };

  const busy = isSubmitting || navigating;
  const credentialsRejected = failure?.title === INVALID_CREDENTIALS;

  return (
    <form noValidate onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-[18px]">
      {failure && (
        <Notice tone="error" title={failure.title} compact>
          {failure.text}
        </Notice>
      )}
      <fieldset disabled={busy} className="contents">
        <Input
          label="Correo electrónico"
          type="email"
          autoComplete="email"
          size="lg"
          error={errors.email?.message}
          aria-invalid={credentialsRejected || undefined}
          {...register("email")}
        />
        <Input
          label="Contraseña"
          type="password"
          autoComplete="current-password"
          size="lg"
          error={errors.password?.message}
          aria-invalid={credentialsRejected || undefined}
          {...register("password")}
        />
      </fieldset>
      <Button type="submit" variant="primary" size="lg" full loading={busy}>
        {busy ? "Iniciando sesión…" : "Iniciar sesión"}
      </Button>
    </form>
  );
}
