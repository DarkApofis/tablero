import { z } from "zod";
import type { components, paths } from "./api-schema";

export type Schemas = components["schemas"];
export type Task = Schemas["Task"];
export type ErrorCode = Schemas["ErrorCode"];

export const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

// The refresh token lives in an HttpOnly cookie (credentials: "include"), but every
// route except the SSE stream requires the access token in the Authorization header.
let accessToken: string | null = null;

export function setAccessToken(token: string | null) {
  accessToken = token;
}

/* ---------- Types derived from the OpenAPI spec ---------- */

type Method = "get" | "post" | "put" | "patch" | "delete";

type PathsWith<M extends Method> = {
  [P in keyof paths]: paths[P][M] extends { responses: unknown } ? P : never;
}[keyof paths];

type Op<P extends keyof paths, M extends Method> = Extract<paths[P][M], { responses: unknown }>;

type PathParams<O> = O extends { parameters: { path: infer X } } ? X : never;
type QueryParams<O> = O extends { parameters: { query?: infer Q } } ? NonNullable<Q> : never;
type Body<O> = O extends { requestBody: { content: { "application/json": infer B } } } ? B : never;

type Json<R> = R extends { content: { "application/json": infer B } } ? B : undefined;
type Success<O> = O extends { responses: infer R }
  ? { [S in keyof R]: S extends 200 | 201 | 202 | 204 ? Json<R[S]> : never }[keyof R]
  : never;

type Options<O> = ([PathParams<O>] extends [never] ? { path?: never } : { path: PathParams<O> }) &
  ([QueryParams<O>] extends [never] ? { query?: never } : { query?: QueryParams<O> }) &
  ([Body<O>] extends [never] ? { body?: never } : { body: Body<O> }) & {
    signal?: AbortSignal;
    headers?: HeadersInit;
  };

// Options are optional only when the operation needs neither path params nor a body.
type Args<O> = object extends Options<O> ? [options?: Options<O>] : [options: Options<O>];

/* ---------- Errors ---------- */

export type ApiErrorCode = ErrorCode | "network_error" | "unexpected_response";

const errorBody = z.object({
  error: z.object({
    code: z.string(),
    message: z.string(),
    fields: z.record(z.string(), z.string()).optional(),
  }),
  // Only on 409 (version_conflict / neighbors_changed): the task as it is now.
  current: z.looseObject({ id: z.string(), version: z.number() }).optional(),
});

export class ApiError extends Error {
  readonly status: number;
  readonly code: ApiErrorCode;
  readonly fields?: Record<string, string>;
  readonly current?: Task;
  readonly requestId: string | null;

  constructor(init: {
    status: number;
    code: ApiErrorCode;
    message: string;
    fields?: Record<string, string>;
    current?: Task;
    requestId?: string | null;
  }) {
    super(init.message);
    this.name = "ApiError";
    this.status = init.status;
    this.code = init.code;
    this.fields = init.fields;
    this.current = init.current;
    this.requestId = init.requestId ?? null;
  }
}

/** A 409 that carries the task's current state, ready to reconcile and retry with `current.version`. */
export function isTaskConflict(err: unknown): err is ApiError & { current: Task } {
  return err instanceof ApiError && err.status === 409 && err.current !== undefined;
}

async function toApiError(res: Response): Promise<ApiError> {
  const requestId = res.headers.get("X-Request-ID");
  const parsed = errorBody.safeParse(await res.json().catch(() => null));
  if (!parsed.success) {
    return new ApiError({
      status: res.status,
      code: "unexpected_response",
      message: res.statusText || `HTTP ${res.status}`,
      requestId,
    });
  }
  const { error, current } = parsed.data;
  return new ApiError({
    status: res.status,
    code: error.code as ErrorCode,
    message: error.message,
    fields: error.fields,
    current: res.status === 409 ? (current as Task | undefined) : undefined,
    requestId,
  });
}

/* ---------- Client ---------- */

type RawOptions = {
  path?: Record<string, string>;
  query?: Record<string, string | number | boolean | undefined>;
  body?: unknown;
  signal?: AbortSignal;
  headers?: HeadersInit;
};

function buildUrl(template: string, opts: RawOptions): string {
  const path = template.replace(/\{(\w+)\}/g, (_, name: string) => {
    const value = opts.path?.[name];
    if (value === undefined) throw new Error(`Falta el parámetro de ruta "${name}" en ${template}`);
    return encodeURIComponent(value);
  });
  const url = new URL(path, API_URL);
  for (const [key, value] of Object.entries(opts.query ?? {})) {
    if (value !== undefined) url.searchParams.set(key, String(value));
  }
  return url.toString();
}

async function request(method: Method, template: string, opts: RawOptions = {}): Promise<unknown> {
  const headers = new Headers(opts.headers);
  headers.set("Accept", "application/json");
  if (opts.body !== undefined) headers.set("Content-Type", "application/json");
  if (accessToken) headers.set("Authorization", `Bearer ${accessToken}`);

  let res: Response;
  try {
    res = await fetch(buildUrl(template, opts), {
      method: method.toUpperCase(),
      headers,
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      credentials: "include",
      signal: opts.signal,
    });
  } catch (err) {
    if (opts.signal?.aborted) throw err;
    throw new ApiError({
      status: 0,
      code: "network_error",
      message: "No se pudo conectar con el servidor",
    });
  }

  if (!res.ok) throw await toApiError(res);
  if (res.status === 204 || !res.headers.get("Content-Type")?.includes("application/json")) {
    return undefined;
  }
  return res.json();
}

function method<M extends Method>(m: M) {
  return <P extends PathsWith<M>>(path: P, ...[options]: Args<Op<P, M>>) =>
    request(m, path, options as RawOptions) as Promise<Success<Op<P, M>>>;
}

/**
 * Typed client for the Tablero API. Paths, params, bodies and responses come from
 * docs/openapi.yaml (`pnpm gen:api`).
 *
 * @example
 * const task = await api.patch("/tasks/{id}", { path: { id }, body: { title, version } });
 */
export const api = {
  get: method("get"),
  post: method("post"),
  put: method("put"),
  patch: method("patch"),
  delete: method("delete"),
};
