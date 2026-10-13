import { APIError, secretInputSchema } from "./api";
import type {
  Application,
  AppInput,
  ConsoleAPI,
  Provider,
  Secret,
  SecretInput,
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
        {
          id: `sec_${index}_current`,
          name: "Production backend",
          description: "Main product service.\nOwner: Platform team",
          created_at: initialDate,
        },
        {
          id: `sec_${index}_staging`,
          name: "Staging",
          description: "",
          created_at: initialDate,
        },
        {
          id: `sec_${index}_unicode`,
          name: "开发环境・Development " + "🔑".repeat(16),
          description:
            "  Deployment notes\n" +
            "详细说明与负责人🔑".repeat(24) +
            "\n  Preserve these spaces.  ",
          created_at: initialDate,
        },
        {
          id: `sec_${index}_previous`,
          name: "Retired deployment",
          description: "Replaced after deployment migration.",
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
  const validateSecret = (input: SecretInput) => {
    const parsed = secretInputSchema.safeParse(input);
    if (!parsed.success)
      throw new APIError(400, "invalid_input", parsed.error.issues[0].message);
  };
  const issueSecret = (id: string, input: SecretInput) => {
    validateSecret(input);
    getApp(id);
    const issued = {
      id: `sec_preview_${sequence++}`,
      ...input,
      secret: `${crypto.randomUUID()}${crypto.randomUUID()}`.replaceAll(
        "-",
        "",
      ),
      created_at: new Date().toISOString(),
    };
    const existing = credentials.get(id);
    if (existing === undefined)
      throw new Error("Preview application credentials are missing.");
    credentials.set(id, [
      ...existing,
      {
        id: issued.id,
        name: issued.name,
        description: issued.description,
        created_at: issued.created_at,
      },
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
      validateSecret(input.initial_secret);
      const { initial_secret, ...configuration } = input;
      const now = new Date().toISOString();
      const app: Application = {
        ...configuration,
        id: `app_preview_${sequence++}`,
        created_at: now,
        updated_at: now,
      };
      apps = [...apps, app];
      credentials.set(app.id, []);
      const issued = issueSecret(app.id, initial_secret);
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
    createSecret: async (id, input) => {
      await wait();
      authorize();
      return issueSecret(id, input);
    },
    updateSecret: async (id, secretID, input) => {
      await wait();
      authorize();
      getApp(id);
      validateSecret(input);
      const secrets = credentials.get(id);
      const secret = secrets?.find((item) => item.id === secretID);
      if (secrets === undefined || secret === undefined)
        throw new APIError(404, "not_found", "The secret was not found.");
      const updated = { ...secret, ...input };
      credentials.set(
        id,
        secrets.map((item) => (item.id === secretID ? updated : item)),
      );
      return structuredClone(updated);
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
