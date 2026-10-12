import { z } from "zod";

export const providerID = z.enum(["microsoft", "google", "github"]);
export type ProviderID = z.infer<typeof providerID>;
const timestamp = z.iso.datetime({ offset: true });
export const appSchema = z.object({
  id: z.string().min(1),
  name: z.string().min(1),
  enabled: z.boolean(),
  redirect_uris: z.array(z.string()),
  providers: z.array(providerID),
  created_at: timestamp,
  updated_at: timestamp,
});
export type Application = z.infer<typeof appSchema>;
export type AppInput = Pick<
  Application,
  "name" | "enabled" | "redirect_uris" | "providers"
>;
const secretSchema = z.object({
  id: z.string().min(1),
  created_at: timestamp,
  revoked_at: timestamp.optional(),
});
export type Secret = z.infer<typeof secretSchema>;
const issuedSchema = z.object({
  id: z.string().min(1),
  secret: z.string().min(1),
  created_at: timestamp,
});
export type IssuedSecret = z.infer<typeof issuedSchema>;
const createdSchema = z.object({
  app: appSchema,
  secret: z.string().min(1),
  secret_id: z.string().min(1),
});
export type CreatedApp = z.infer<typeof createdSchema>;
const providerSchema = z.object({ id: providerID, available: z.boolean() });
export type Provider = z.infer<typeof providerSchema>;
const sessionSchema = z.object({ csrf_token: z.string().min(1) });
export type Session = z.infer<typeof sessionSchema>;
const errorSchema = z.object({
  error: z.object({ code: z.string(), message: z.string().min(1) }),
});

export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = "APIError";
  }
}

export interface ConsoleAPI {
  session: () => Promise<Session>;
  login: (password: string) => Promise<Session>;
  logout: () => Promise<void>;
  listApps: () => Promise<Application[]>;
  listProviders: () => Promise<Provider[]>;
  createApp: (input: AppInput) => Promise<CreatedApp>;
  updateApp: (id: string, input: AppInput) => Promise<Application>;
  deleteApp: (id: string) => Promise<void>;
  listSecrets: (id: string) => Promise<Secret[]>;
  createSecret: (id: string) => Promise<IssuedSecret>;
  revokeSecret: (id: string, secretID: string) => Promise<void>;
}

export const createAPI = (fetcher: typeof fetch = fetch): ConsoleAPI => {
  let csrf: string | undefined;
  const request = async (
    path: string,
    method = "GET",
    body?: unknown,
  ): Promise<unknown> => {
    const headers: Record<string, string> = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (method !== "GET" && csrf !== undefined) headers["X-CSRF-Token"] = csrf;
    const response = await fetcher(`/console/api${path}`, {
      method,
      headers,
      credentials: "same-origin",
      cache: "no-store",
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (response.status === 204) return undefined;
    const result: unknown = await response.json();
    if (!response.ok) {
      const { error } = errorSchema.parse(result);
      throw new APIError(response.status, error.code, error.message);
    }
    return result;
  };
  const rememberSession = (result: unknown): Session => {
    const session = sessionSchema.parse(result);
    csrf = session.csrf_token;
    return session;
  };
  const appPath = (id: string) => `/apps/${encodeURIComponent(id)}`;
  return {
    session: async () => rememberSession(await request("/session")),
    login: async (password) =>
      rememberSession(await request("/login", "POST", { password })),
    logout: async () => {
      await request("/logout", "POST");
      csrf = undefined;
    },
    listApps: async () =>
      z.object({ apps: z.array(appSchema) }).parse(await request("/apps")).apps,
    listProviders: async () =>
      z
        .object({ providers: z.array(providerSchema) })
        .parse(await request("/providers")).providers,
    createApp: async (input) =>
      createdSchema.parse(await request("/apps", "POST", input)),
    updateApp: async (id, input) =>
      appSchema.parse(await request(appPath(id), "PUT", input)),
    deleteApp: async (id) => {
      await request(appPath(id), "DELETE");
    },
    listSecrets: async (id) =>
      z
        .object({ secrets: z.array(secretSchema) })
        .parse(await request(`${appPath(id)}/secrets`)).secrets,
    createSecret: async (id) =>
      issuedSchema.parse(await request(`${appPath(id)}/secrets`, "POST")),
    revokeSecret: async (id, secretID) => {
      await request(
        `${appPath(id)}/secrets/${encodeURIComponent(secretID)}`,
        "DELETE",
      );
    },
  };
};
