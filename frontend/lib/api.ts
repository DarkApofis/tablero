import type { components, paths } from "./api-schema";
import { clearSignedIn, isSignedIn, markSignedIn } from "./session";

export type Schemas = components["schemas"];
export type Task = Schemas["Task"];
export type User = Schemas["User"];
export type Session = Schemas["Session"];
export type ErrorCode = Schemas["ErrorCode"];

/**
 * The API as the browser reaches it: an absolute URL ("http://localhost:8080"), or
 * a path on this site ("/api") when the frontend forwards it to the API itself
 * (next.config.ts, API_PROXY_TARGET), which keeps the API's cookies first-party.
 */
export const API_URL = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

/**
 * The URL of an API path. Unlike `new URL(path, base)`, it keeps the base's own
 * path: with base "/api", "/auth/refresh" is "/api/auth/refresh".
 */
export function apiUrl(path: string, base: string = API_URL): URL {
  const url = new URL(base, typeof window === "undefined" ? "http://localhost" : window.location.origin);
  url.pathname = url.pathname.replace(/\/$/, "") + path;
  return url;
}

/* ---------- Session ---------- */

// The refresh token lives in an HttpOnly cookie (credentials: "include"), but every
// route except the SSE stream requires the access token in the Authorization header.
// It is kept in memory only: after a reload the first request gets a 401 and renews it.
let accessToken: string | null = null;

export function startSession(session: Session) {
  accessToken = session.access_token;
  markSignedIn();
}

export function clearSession() {
  accessToken = null;
  clearSignedIn();
}

const expiryListeners = new Set<() => void>();

/** Runs `listener` when the session can no longer be renewed. Returns an unsubscribe. */
export function onSessionExpired(listener: () => void) {
  expiryListeners.add(listener);
  return () => void expiryListeners.delete(listener);
}

let refreshing: Promise<boolean> | null = null;

/**
 * Renews the access token with the refresh cookie. Resolves false if the session is
 * over (and notifies onSessionExpired); throws ApiError if the API could not answer,
 * which leaves the session alone.
 *
 * Concurrent callers in this tab share one request. Across tabs, a Web Lock makes
 * renewals take turns: each rotation replaces the cookie, so two tabs presenting the
 * same refresh token at once would make the API reject the second one.
 */
export function refreshSession(): Promise<boolean> {
  refreshing ??= withRefreshLock(renew).finally(() => {
    refreshing = null;
  });
  return refreshing;
}

// After a reload the page needs a token before any data: start renewing as soon as
// this module runs, while React is still hydrating, rather than when the first
// query fires. request() waits for the same renewal.
if (typeof window !== "undefined" && isSignedIn()) {
  refreshSession().catch(() => {});
}

async function withRefreshLock<T>(fn: () => Promise<T>): Promise<T> {
  if (typeof navigator === "undefined" || !navigator.locks) return fn();
  return await navigator.locks.request("tablero:refresh", fn);
}

async function renew(): Promise<boolean> {
  let res: Response;
  try {
    res = await fetch(apiUrl("/auth/refresh"), {
      method: "POST",
      headers: { Accept: "application/json" },
      credentials: "include",
    });
  } catch {
    throw networkError();
  }
  if (res.status === 401) {
    clearSession();
    for (const listener of expiryListeners) listener();
    return false;
  }
  if (!res.ok) throw await toApiError(res);
  startSession((await res.json()) as Session);
  return true;
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

type ErrorBody = {
  error: { code: string; message: string; fields?: Record<string, string> };
  // Only on 409 (version_conflict / neighbors_changed): the task as it is now.
  current?: Task;
};

// Checked by hand rather than with zod: this module ships on every page.
function parseErrorBody(body: unknown): ErrorBody | null {
  if (!isObject(body) || !isObject(body.error)) return null;
  const { code, message, fields } = body.error;
  if (typeof code !== "string" || typeof message !== "string") return null;
  if (fields !== undefined && !(isObject(fields) && Object.values(fields).every((v) => typeof v === "string"))) {
    return null;
  }
  const current = body.current;
  if (current !== undefined && !(isObject(current) && typeof current.id === "string" && typeof current.version === "number")) {
    return null;
  }
  return body as ErrorBody;
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

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
  const parsed = parseErrorBody(await res.json().catch(() => null));
  if (!parsed) {
    return new ApiError({
      status: res.status,
      code: "unexpected_response",
      message: res.statusText || `HTTP ${res.status}`,
      requestId,
    });
  }
  const { error, current } = parsed;
  return new ApiError({
    status: res.status,
    code: error.code as ErrorCode,
    message: error.message,
    fields: error.fields,
    current: res.status === 409 ? current : undefined,
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
  const url = apiUrl(path);
  for (const [key, value] of Object.entries(opts.query ?? {})) {
    if (value !== undefined) url.searchParams.set(key, String(value));
  }
  return url.toString();
}

function networkError() {
  return new ApiError({ status: 0, code: "network_error", message: "No se pudo conectar con el servidor" });
}

async function request(method: Method, template: string, opts: RawOptions = {}, retried = false): Promise<unknown> {
  // After a reload the token is gone but the session isn't: renew first rather than
  // send the request, get a 401 and send it again. That round trip delayed every
  // first paint with data. Concurrent requests share the one renewal.
  if (accessToken === null && !retried && !template.startsWith("/auth/") && isSignedIn()) {
    try {
      await refreshSession();
    } catch {
      // The API didn't answer; the request below will say so.
    }
    retried = true;
  }

  const headers = new Headers(opts.headers);
  headers.set("Accept", "application/json");
  if (opts.body !== undefined) headers.set("Content-Type", "application/json");
  const tokenUsed = accessToken;
  if (tokenUsed) headers.set("Authorization", `Bearer ${tokenUsed}`);

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
    throw networkError();
  }

  // An expired access token: renew it once and retry. /auth/* answers 401 for bad
  // credentials or a dead refresh token, which a renewal cannot fix.
  if (res.status === 401 && !retried && !template.startsWith("/auth/")) {
    // Another request may have renewed the token while this one was in flight.
    const renewed = (accessToken !== null && accessToken !== tokenUsed) || (await refreshSession());
    if (renewed) return request(method, template, opts, true);
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
