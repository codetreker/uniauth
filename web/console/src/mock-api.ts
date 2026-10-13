import { APIError } from "./api";
import type {
  Application,
  AppInput,
  ConsoleAPI,
  Provider,
  Secret,
} from "./api";

export const createMockAPI = (): ConsoleAPI => {
  let authenticated = true;
  let sequence = 4;
  const initialDate = "2026-10-12T09:30:00Z";
  let apps: Application[] = [
    {
      id: "app_BVg4rKX8",
      name: "Borgee",
      enabled: true,
      redirect_uris: [
        "https://borgee.example/auth/callback",
        "https://staging.borgee.example/auth/callback",
      ],
      providers: ["microsoft", "google", "github"],
      created_at: "2026-10-08T09:30:00Z",
      updated_at: initialDate,
    },
    {
      id: "app_Sf9nT2Qp",
      name: "Syntrix",
      enabled: true,
      redirect_uris: ["https://syntrix.example/auth/callback"],
      providers: ["google", "github"],
      created_at: "2026-10-09T09:30:00Z",
      updated_at: initialDate,
    },
    {
      id: "app_Km3cH7Wd",
      name: "Biz Table",
      enabled: false,
      redirect_uris: ["https://biztable.example/auth/callback"],
      providers: ["microsoft"],
      created_at: "2026-10-10T09:30:00Z",
      updated_at: "2026-10-11T15:00:00Z",
    },
  ];
  const credentials = new Map<string, Secret[]>(
    apps.map((app, index) => [
      app.id,
      [
        { id: `sec_${index}_current`, created_at: initialDate },
        {
          id: `sec_${index}_previous`,
          created_at: "2026-10-08T09:30:00Z",
          revoked_at: initialDate,
        },
      ],
    ]),
  );
  const providers: Provider[] = [
    { id: "microsoft", available: true },
    { id: "google", available: true },
    { id: "github", available: true },
  ];
  const wait = async () => {
    await new Promise((resolve) => setTimeout(resolve, 180));
  };
  const authorize = () => {
    if (!authenticated)
      throw new APIError(
        401,
        "unauthorized",
        "Administrator authentication is required.",
      );
  };
  const getApp = (id: string) => {
    const app = apps.find((item) => item.id === id);
    if (app === undefined)
      throw new APIError(404, "not_found", "The application was not found.");
    return app;
  };
  const validate = (input: AppInput) => {
    if (
      input.name.length === 0 ||
      input.providers.length === 0 ||
      input.redirect_uris.length === 0
    )
      throw new APIError(
        400,
        "invalid_input",
        "The application configuration is invalid.",
      );
    for (const value of input.redirect_uris) {
      const url = new URL(value);
      if (
        url.protocol !== "https:" ||
        url.username !== "" ||
        url.password !== "" ||
        url.hash !== "" ||
        ["code", "state", "error"].some((key) => url.searchParams.has(key))
      )
        throw new APIError(
          400,
          "invalid_input",
          "Callback URLs must use HTTPS without user information, fragments, or reserved parameters.",
        );
    }
  };
  const newSecret = () => ({
    id: `sec_preview_${sequence++}`,
    secret: `${crypto.randomUUID()}${crypto.randomUUID()}`.replaceAll("-", ""),
    created_at: new Date().toISOString(),
  });
  const issueSecret = (id: string) => {
    getApp(id);
    const issued = newSecret();
    const existing = credentials.get(id);
    if (existing === undefined)
      throw new Error("Preview application credentials are missing.");
    credentials.set(id, [
      ...existing,
      { id: issued.id, created_at: issued.created_at },
    ]);
    return issued;
  };
  return {
    session: async () => {
      await wait();
      authorize();
      return { csrf_token: "preview-session" };
    },
    login: async (password) => {
      await wait();
      if (password !== "preview")
        throw new APIError(
          401,
          "unauthorized",
          "The administrator password is incorrect.",
        );
      authenticated = true;
      return { csrf_token: "preview-session" };
    },
    logout: async () => {
      await wait();
      authorize();
      authenticated = false;
    },
    listApps: async () => {
      await wait();
      authorize();
      return structuredClone(apps);
    },
    listProviders: async () => {
      await wait();
      authorize();
      return structuredClone(providers);
    },
    createApp: async (input) => {
      await wait();
      authorize();
      validate(input);
      const now = new Date().toISOString();
      const app: Application = {
        ...input,
        id: `app_preview_${sequence++}`,
        created_at: now,
        updated_at: now,
      };
      apps = [...apps, app];
      credentials.set(app.id, []);
      const issued = issueSecret(app.id);
      return {
        app: structuredClone(app),
        secret: issued.secret,
        secret_id: issued.id,
      };
    },
    updateApp: async (id, input) => {
      await wait();
      authorize();
      validate(input);
      const updated = {
        ...getApp(id),
        ...input,
        updated_at: new Date().toISOString(),
      };
      apps = apps.map((app) => (app.id === id ? updated : app));
      return structuredClone(updated);
    },
    deleteApp: async (id) => {
      await wait();
      authorize();
      getApp(id);
      apps = apps.filter((app) => app.id !== id);
      credentials.delete(id);
    },
    listSecrets: async (id) => {
      await wait();
      authorize();
      getApp(id);
      const secrets = credentials.get(id);
      if (secrets === undefined)
        throw new Error("Preview application credentials are missing.");
      return structuredClone(secrets);
    },
    createSecret: async (id) => {
      await wait();
      authorize();
      return issueSecret(id);
    },
    resetSecret: async (id, secretID) => {
      await wait();
      authorize();
      getApp(id);
      const secrets = credentials.get(id);
      if (secrets === undefined)
        throw new Error("Preview application credentials are missing.");
      const target = secrets.find((secret) => secret.id === secretID);
      if (target === undefined)
        throw new APIError(404, "not_found", "The secret was not found.");
      if (target.revoked_at !== undefined)
        throw new APIError(
          409,
          "already_revoked",
          "The secret is already revoked.",
        );
      const issued = newSecret();
      credentials.set(id, [
        ...secrets.map((secret) =>
          secret.id === secretID
            ? { ...secret, revoked_at: issued.created_at }
            : secret,
        ),
        { id: issued.id, created_at: issued.created_at },
      ]);
      return issued;
    },
    revokeSecret: async (id, secretID) => {
      await wait();
      authorize();
      getApp(id);
      const secrets = credentials.get(id);
      if (
        secrets === undefined ||
        !secrets.some((secret) => secret.id === secretID)
      )
        throw new APIError(404, "not_found", "The secret was not found.");
      credentials.set(
        id,
        secrets.map((secret) =>
          secret.id === secretID
            ? { ...secret, revoked_at: new Date().toISOString() }
            : secret,
        ),
      );
    },
  };
};
