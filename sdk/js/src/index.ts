import { Buffer } from "node:buffer";
import { randomBytes } from "node:crypto";
import { APIError, CallbackError, ProtocolError } from "./errors.js";
import {
  requireText,
  validText,
  validateBaseURL,
  validateRedirectURI,
} from "./validation.js";

export { APIError, CallbackError, ProtocolError } from "./errors.js";
export type { CallbackErrorCode } from "./errors.js";

export type Provider = "microsoft" | "google" | "github";

export interface Identity {
  readonly provider: Provider;
  readonly id: string;
  readonly email: string | null;
  readonly email_verified: boolean | null;
  readonly name: string | null;
  readonly avatar_url: string | null;
  /** Native JavaScript numbers can lose precision; rawJSON is authoritative. */
  readonly origin: Record<string, unknown>;
  /** The entire exact UTF-8 JSON response, including unknown envelope fields. */
  readonly rawJSON: string;
}

export interface ClientOptions {
  readonly baseURL: string;
  readonly appID: string;
  readonly appSecret: string;
  readonly allowLoopbackHTTP?: boolean;
  readonly fetch?: typeof globalThis.fetch;
  /** Integer milliseconds from 1 through 2,147,483,647; defaults to 30,000. */
  readonly timeoutMs?: number;
}

export interface AuthorizationOptions {
  readonly provider: Provider;
  readonly redirectURI: string;
  readonly state: string;
}

export interface RetrieveOptions {
  readonly signal?: AbortSignal;
}

export interface Client {
  authorizationURL(options: AuthorizationOptions): string;
  /** Sends one request. A lost response cannot be recovered by retrying. */
  retrieve(code: string, options?: RetrieveOptions): Promise<Identity>;
}

const provider = (value: unknown): value is Provider =>
  value === "microsoft" || value === "google" || value === "github";

const object = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null && !Array.isArray(value);

const nullableText = (value: unknown): value is string | null =>
  value === null || (typeof value === "string" && value.isWellFormed());

const nullableBoolean = (value: unknown): value is boolean | null =>
  value === null || typeof value === "boolean";

export const generateState = (): string =>
  randomBytes(32).toString("base64url");

/**
 * Validates decoded callback parameters. The product must atomically validate
 * and consume its browser-bound pending state before calling retrieve.
 */
export const parseCallback = (
  query: URLSearchParams,
  expectedState: string,
): string => {
  const states = query.getAll("state");
  if (
    states.length !== 1 ||
    !validText(states[0], 2048) ||
    !validText(expectedState, 2048)
  ) {
    throw new CallbackError("invalid_callback");
  }
  if (states[0] !== expectedState) throw new CallbackError("state_mismatch");

  const codes = query.getAll("code");
  const errors = query.getAll("error");
  if (codes.length === 1 && errors.length === 0 && validText(codes[0], 512)) {
    return codes[0];
  }
  if (errors.length === 1 && codes.length === 0 && validText(errors[0], 256)) {
    const code = errors[0];
    if (
      code === "provider_denied" ||
      code === "provider_failed" ||
      code === "capacity_exceeded"
    ) {
      throw new CallbackError(code);
    }
  }
  throw new CallbackError("invalid_callback");
};

const decodeResponse = async (response: Response): Promise<Identity> => {
  const status = response.status;
  const requestID = response.headers.get("X-Request-ID");
  const fail = (message: string): never => {
    throw new ProtocolError(message, status, requestID);
  };

  // Read failures remain the original transport error; parse diagnostics must
  // never retain credentials, result codes, or identity JSON from the body.
  const bytes = await response.arrayBuffer();
  if (
    response.redirected ||
    (status !== 200 && (status < 400 || status > 599))
  ) {
    return fail("The result endpoint returned an unexpected HTTP status.");
  }
  const contentType = response.headers.get("Content-Type");
  if (
    contentType?.split(";", 1)[0]?.trim().toLowerCase() !== "application/json"
  ) {
    return fail("The result endpoint did not return application/json.");
  }

  let rawJSON: string;
  let parsed: unknown;
  try {
    rawJSON = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(
      bytes,
    );
    parsed = JSON.parse(rawJSON);
  } catch {
    return fail("The result endpoint returned invalid JSON.");
  }
  if (status !== 200) {
    if (
      !object(parsed) ||
      !object(parsed.error) ||
      typeof parsed.error.code !== "string" ||
      !parsed.error.code.isWellFormed() ||
      parsed.error.code.length === 0 ||
      typeof parsed.error.message !== "string" ||
      !parsed.error.message.isWellFormed() ||
      parsed.error.message.length === 0
    ) {
      return fail("The result endpoint returned an invalid error response.");
    }
    throw new APIError(
      status,
      parsed.error.code,
      parsed.error.message,
      requestID,
    );
  }
  if (
    !object(parsed) ||
    !provider(parsed.provider) ||
    typeof parsed.id !== "string" ||
    !parsed.id.isWellFormed() ||
    parsed.id.length === 0 ||
    !nullableText(parsed.email) ||
    !nullableBoolean(parsed.email_verified) ||
    !nullableText(parsed.name) ||
    !nullableText(parsed.avatar_url) ||
    !object(parsed.origin)
  ) {
    return fail("The result endpoint returned an invalid identity envelope.");
  }
  return {
    provider: parsed.provider,
    id: parsed.id,
    email: parsed.email,
    email_verified: parsed.email_verified,
    name: parsed.name,
    avatar_url: parsed.avatar_url,
    origin: parsed.origin,
    rawJSON,
  };
};

export const createClient = (options: ClientOptions): Client => {
  const appID = requireText(options.appID, 128, "appID");
  if (appID.includes(":"))
    throw new TypeError("appID must not contain a colon.");
  const secret = requireText(options.appSecret, 512, "appSecret");
  const allowLoopbackHTTP =
    options.allowLoopbackHTTP === undefined ? false : options.allowLoopbackHTTP;
  if (typeof allowLoopbackHTTP !== "boolean") {
    throw new TypeError("allowLoopbackHTTP must be a boolean.");
  }
  const baseURL = validateBaseURL(options.baseURL, allowLoopbackHTTP);
  const fetcher =
    options.fetch === undefined ? globalThis.fetch : options.fetch;
  if (typeof fetcher !== "function")
    throw new TypeError("fetch must be a function.");
  const timeoutMs =
    options.timeoutMs === undefined ? 30_000 : options.timeoutMs;
  if (
    !Number.isInteger(timeoutMs) ||
    timeoutMs <= 0 ||
    timeoutMs > 2_147_483_647
  ) {
    throw new TypeError(
      "timeoutMs must be a positive integer up to 2147483647.",
    );
  }
  const authorization = `Basic ${Buffer.from(`${appID}:${secret}`, "utf8").toString("base64")}`;
  const resultURL = new URL("/auth/result", baseURL).href;

  return Object.freeze({
    authorizationURL: ({
      provider: name,
      redirectURI,
      state,
    }: AuthorizationOptions) => {
      if (!provider(name)) throw new TypeError("provider is unsupported.");
      requireText(redirectURI, 2048, "redirectURI");
      validateRedirectURI(redirectURI);
      requireText(state, 2048, "state");
      const url = new URL("/auth/authorize", baseURL);
      url.searchParams.set("app_id", appID);
      url.searchParams.set("provider", name);
      url.searchParams.set("redirect_uri", redirectURI);
      url.searchParams.set("state", state);
      return url.href;
    },
    retrieve: async (code: string, { signal }: RetrieveOptions = {}) => {
      requireText(code, 512, "code");
      const timeout = AbortSignal.timeout(timeoutMs);
      const requestSignal =
        signal === undefined ? timeout : AbortSignal.any([signal, timeout]);
      const response = await fetcher(resultURL, {
        method: "POST",
        headers: {
          Authorization: authorization,
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        body: JSON.stringify({ code }),
        redirect: "manual",
        signal: requestSignal,
      });
      return decodeResponse(response);
    },
  });
};
