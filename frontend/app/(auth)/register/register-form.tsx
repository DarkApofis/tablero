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
import { meQueryKey, register as registerAccount } from "@/lib/auth";
import { charCount, describeError, nextPath } from "../form-errors";
import { useFocusAfterSubmit } from "../use-focus-after-submit";

// Mirrors domain.NewRegistration in the API, with its messages.
const schema = z.object({
  name: z
    .string()
    .trim()
    .min(1, "El nombre es obligatorio")
    .refine((s) => charCount(s) <= 100, "El nombre no puede superar 100 caracteres"),
  email: z.string().trim().max(254, "Introduce un correo válido").pipe(z.email("Introduce un correo válido")),
  password: z
    .string()
    .refine((s) => charCount(s) >= 8, "La contraseña debe tener al menos 8 caracteres")
    .refine((s) => charCount(s) <= 128, "La contraseña no puede superar 128 caracteres"),
});

type Values = z.infer<typeof schema>;
const fieldNames = ["name", "email", "password"] as const;

export function RegisterForm() {
  const router = useRouter();
  const queryClient = useQueryClient();
  const [navigating, startNavigation] = useTransition();
  const [failure, setFailure] = useState<string | null>(null);
  const {
    register,
    handleSubmit,
    setError,
    setFocus,
    formState: { errors, isSubmitting },
  } = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { name: "", email: "", password: "" } });
  const focusLater = useFocusAfterSubmit(isSubmitting, setFocus);

  const onSubmit = async (values: Values) => {
    setFailure(null);
    try {
      const user = await registerAccount(values);
      queryClient.setQueryData(meQueryKey, user);
      startNavigation(() => router.replace(nextPath()));
    } catch (err) {
      if (err instanceof ApiError && err.code === "email_taken") {
        setError("email", { message: err.message });
        focusLater("email");
      } else if (err instanceof ApiError && err.code === "validation_error" && err.fields) {
        const invalid = fieldNames.filter((name) => err.fields?.[name]);
        for (const name of invalid) setError(name, { message: err.fields[name] });
        if (invalid[0]) focusLater(invalid[0]);
      } else {
        setFailure(describeError(err));
      }
    }
  };

  const busy = isSubmitting || navigating;

  return (
    <form noValidate onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-[18px]">
      {failure && (
        <Notice tone="error" title="No pudimos crear la cuenta" compact>
          {failure}
        </Notice>
      )}
      <fieldset disabled={busy} className="contents">
        <Input label="Nombre" autoComplete="name" size="lg" error={errors.name?.message} {...register("name")} />
        <Input
          label="Correo electrónico"
          type="email"
          autoComplete="email"
          size="lg"
          error={errors.email?.message}
          {...register("email")}
        />
        <Input
          label="Contraseña"
          type="password"
          autoComplete="new-password"
          placeholder="Mínimo 8 caracteres"
          size="lg"
          error={errors.password?.message}
          {...register("password")}
        />
      </fieldset>
      <Button type="submit" variant="primary" size="lg" full loading={busy}>
        {busy ? "Creando cuenta…" : "Crear cuenta"}
      </Button>
    </form>
  );
}
