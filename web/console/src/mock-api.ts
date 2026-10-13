import { APIError, secretInputSchema } from "./api";
import type {
  Account,
  LinkedIdentity,
  ProviderID,
  Session,
  Application,
  AppInput,
  ConsoleAPI,
  Provider,
  Secret,
  SecretInput,
} from "./api";

export type PreviewPersona = "administrator" | "developer" | "new-account";
export type MockConsoleAPI = ConsoleAPI & {
  completeAuthorization: (url: string) => Promise<Session>;
};
export const createMockAPI = (
  persona: PreviewPersona = "administrator",
  signedOut = false,
): MockConsoleAPI => {
  let authenticated = !signedOut;
  const profiles: Record<PreviewPersona, Account> = {
    administrator: {
      id: "usr_platform_admin",
      name: "Jianjun Chen",
      email: "sychenjianjun@gmail.com",
      avatar_url: null,
      is_admin: true,
      created_at: "2026-10-08T09:30:00Z",
    },
    developer: {
      id: "usr_product_developer",
      name: "Alex Morgan",
      email: "alex@example.com",
      avatar_url: null,
      is_admin: false,
      created_at: "2026-10-09T09:30:00Z",
    },
    "new-account": {
      id: "usr_new_member",
      name: null,
      email: null,
      avatar_url: null,
      is_admin: false,
      created_at: "2026-10-13T09:30:00Z",
    },
  };
  const user = profiles[persona];
  const identities: LinkedIdentity[] = [
    {
      provider:
        persona === "administrator"
          ? "google"
          : persona === "developer"
            ? "github"
            : "microsoft",
      id: `provider-stable-${persona}`,
      name: user.name,
      email: user.email,
      email_verified: persona === "new-account" ? null : true,
      linked_at: user.created_at,
    },
  ];
  const currentSession = (): Session => ({
    csrf_token: "preview-session",
    user: structuredClone(user),
  });
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
  if (persona === "developer") apps = apps.slice(1, 2);
  if (persona === "new-account") apps = [];
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
      throw new APIError(401, "unauthorized", "Authentication is required.");
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
  const newSecret = (input: SecretInput) => ({
    id: `sec_preview_${sequence++}`,
    ...input,
    secret: `${crypto.randomUUID()}${crypto.randomUUID()}`.replaceAll("-", ""),
    created_at: new Date().toISOString(),
  });
  const issueSecret = (id: string, input: SecretInput) => {
    validateSecret(input);
    getApp(id);
    const issued = newSecret(input);
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
      return currentSession();
    },
    login: async (provider) => {
      await wait();
      return {
        authorization_url: `https://preview.uniauth.example/authorize?provider=${provider}&action=login`,
      };
    },
    completeAuthorization: async (url) => {
      await wait();
      const authorization = new URL(url);
      if (authorization.origin !== "https://preview.uniauth.example")
        throw new Error("Invalid preview authorization URL.");
      const provider = authorization.searchParams.get("provider") as ProviderID;
      if (!providers.some((item) => item.id === provider && item.available))
        throw new Error("Invalid preview provider.");
      if (authorization.searchParams.get("action") === "link") {
        authorize();
        if (!identities.some((identity) => identity.provider === provider))
          identities.push({
            provider,
            id: `provider-stable-${persona}-${provider}`,
            name: user.name,
            email: user.email,
            email_verified: provider === "microsoft" ? null : true,
            linked_at: new Date().toISOString(),
          });
      } else authenticated = true;
      return currentSession();
    },
    listIdentities: async () => {
      await wait();
      authorize();
      return structuredClone(identities);
    },
    linkIdentity: async (provider) => {
      await wait();
      authorize();
      return {
        authorization_url: `https://preview.uniauth.example/authorize?provider=${provider}&action=link`,
      };
    },
    listUsers: async (cursor) => {
      await wait();
      authorize();
      if (!user.is_admin)
        throw new APIError(
          403,
          "forbidden",
          "Platform administrator access is required.",
        );
      const summaries = Object.values(profiles).map((profile, index) => ({
        id: profile.id,
        email: profile.email,
        name: profile.name,
        created_at: profile.created_at,
        app_count: index === 0 ? apps.length : index === 1 ? 1 : 0,
      }));
      return cursor === undefined
        ? { users: summaries.slice(0, 2), next_cursor: "preview-page-2" }
        : { users: summaries.slice(2), next_cursor: null };
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
      const issued = newSecret({
        name: target.name,
        description: target.description,
      });
      credentials.set(id, [
        ...secrets.map((secret) =>
          secret.id === secretID
            ? { ...secret, revoked_at: issued.created_at }
            : secret,
        ),
        {
          id: issued.id,
          name: issued.name,
          description: issued.description,
          created_at: issued.created_at,
        },
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
