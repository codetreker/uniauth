import { describe, expect, it, vi } from "vitest";
import { APIError, createAPI } from "./api";
import type { Application } from "./api";
import { fixtureUser } from "./test-fixtures";

const app: Application = {
  id: "app_example",
  name: "Example",
  enabled: true,
  redirect_uris: ["https://example.test/callback"],
  providers: ["github"],
  created_at: "2026-10-12T09:00:00Z",
  updated_at: "2026-10-12T09:00:00Z",
};
const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });

describe("Console transport", () => {
  it("creates Apps with optional initial secrets and rejects partial credential responses", async () => {
    const {
      id: _id,
      created_at: _created,
      updated_at: _updated,
      ...input
    } = app;
    const metadata = { name: "Production", description: "" };
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ app }, 201))
      .mockResolvedValueOnce(
        response({ app, secret: "plaintext", secret_id: "sec_new" }, 201),
      )
      .mockResolvedValueOnce(response({ app, secret: "plaintext" }, 201))
      .mockResolvedValueOnce(response({ app, secret_id: "sec_new" }, 201));
    const api = createAPI(fetcher);
    await expect(api.createApp(input)).resolves.toEqual({ app });
    expect(fetcher).toHaveBeenLastCalledWith(
      "/console/api/apps",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify(input),
      }),
    );
    await expect(
      api.createApp({ ...input, initial_secret: metadata }),
    ).resolves.toEqual({
      app,
      secret: "plaintext",
      secret_id: "sec_new",
    });
    expect(fetcher).toHaveBeenLastCalledWith(
      "/console/api/apps",
      expect.objectContaining({
        body: JSON.stringify({ ...input, initial_secret: metadata }),
      }),
    );
    await expect(api.createApp(input)).rejects.toThrow();
    await expect(api.createApp(input)).rejects.toThrow();
  });

  it("sends secret metadata with CSRF and encoded application and secret IDs", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        response({ csrf_token: "session-token", user: fixtureUser }),
      )
      .mockResolvedValueOnce(
        response(
          {
            id: "secret",
            name: "Production",
            description: "  Notes\nOwner  ",
            secret: "plaintext",
            created_at: app.created_at,
          },
          201,
        ),
      );
    const api = createAPI(fetcher);
    await api.session();
    const metadata = { name: "Production", description: "  Notes\nOwner  " };
    await api.createSecret("app/with/slash", metadata);
    expect(fetcher).toHaveBeenLastCalledWith(
      "/console/api/apps/app%2Fwith%2Fslash/secrets",
      expect.objectContaining({
        method: "POST",
        headers: {
          "X-CSRF-Token": "session-token",
          "Content-Type": "application/json",
        },
        body: JSON.stringify(metadata),
        credentials: "same-origin",
      }),
    );
    fetcher.mockResolvedValueOnce(
      response({
        id: "secret/with/slash",
        ...metadata,
        created_at: app.created_at,
        revoked_at: app.created_at,
      }),
    );
    const updated = await api.updateSecret(
      "app/with/slash",
      "secret/with/slash",
      { name: "Retired", description: "" },
    );
    expect(updated.revoked_at).toBe(app.created_at);
    expect(fetcher).toHaveBeenLastCalledWith(
      "/console/api/apps/app%2Fwith%2Fslash/secrets/secret%2Fwith%2Fslash",
      expect.objectContaining({
        method: "PUT",
        headers: {
          "X-CSRF-Token": "session-token",
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ name: "Retired", description: "" }),
      }),
    );
  });

  it("resets the encoded secret ID with a bodyless POST and validates the new credential", async () => {
    const issued = {
      id: "sec_replacement",
      name: "Production",
      description: "Owner: Operations\nProduction service.",
      secret: "new-plaintext",
      created_at: app.created_at,
    };
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        response({ csrf_token: "session-token", user: fixtureUser }),
      )
      .mockResolvedValueOnce(response(issued, 201))
      .mockResolvedValueOnce(response({ ...issued, secret: undefined }, 201));
    const api = createAPI(fetcher);
    await api.session();
    await expect(api.resetSecret("app/with/slash", "sec/old")).resolves.toEqual(
      issued,
    );
    expect(fetcher).toHaveBeenNthCalledWith(
      2,
      "/console/api/apps/app%2Fwith%2Fslash/secrets/sec%2Fold/reset",
      expect.objectContaining({
        method: "POST",
        headers: { "X-CSRF-Token": "session-token" },
        body: undefined,
        credentials: "same-origin",
      }),
    );
    await expect(api.resetSecret(app.id, "sec_old")).rejects.toThrow();
  });

  it("adopts a new session after authentication and clears it on logout", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        response({ csrf_token: "first-token", user: fixtureUser }),
      )
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(
        response({ csrf_token: "new-token", user: fixtureUser }),
      )
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    const api = createAPI(fetcher);
    await api.session();
    await api.logout();
    await api.session();
    await api.deleteApp(app.id);
    expect(fetcher.mock.calls[2][1]?.headers).toEqual({});
    expect(fetcher.mock.calls[3][1]?.headers).toEqual({
      "X-CSRF-Token": "new-token",
    });
  });

  it("preserves explicit API errors and rejects malformed successful responses", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        response(
          {
            error: {
              code: "invalid_csrf",
              message: "The CSRF token is invalid.",
            },
          },
          403,
        ),
      )
      .mockResolvedValueOnce(response({ apps: null }));
    const api = createAPI(fetcher);
    await expect(api.deleteApp(app.id)).rejects.toEqual(
      new APIError(403, "invalid_csrf", "The CSRF token is invalid."),
    );
    await expect(api.listApps()).rejects.toThrow();
  });

  it("requires returned false values and exact app schema rather than inventing data", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ apps: [{ ...app, enabled: false }] }))
      .mockResolvedValueOnce(
        response({ apps: [{ ...app, enabled: undefined }] }),
      );
    const api = createAPI(fetcher);
    expect((await api.listApps())[0].enabled).toBe(false);
    await expect(api.listApps()).rejects.toThrow();
  });
  it("requires explicit metadata in listings, issuance, and updates while preserving empty descriptions", async () => {
    const valid = {
      id: "secret",
      name: "Production",
      description: "",
      created_at: app.created_at,
    };
    const malformed = [
      { ...valid, name: undefined },
      { ...valid, description: undefined },
      { ...valid, name: null },
      { ...valid, description: null },
      { ...valid, name: "" },
      { ...valid, name: " surrounding " },
      { ...valid, name: "Line\nbreak" },
    ];
    for (const secret of malformed) {
      const fetcher = vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(response({ secrets: [secret] }))
        .mockResolvedValueOnce(response({ ...secret, secret: "plaintext" }))
        .mockResolvedValueOnce(response(secret));
      const api = createAPI(fetcher);
      await expect(api.listSecrets(app.id)).rejects.toThrow();
      await expect(
        api.createSecret(app.id, { name: "New", description: "" }),
      ).rejects.toThrow();
      await expect(
        api.updateSecret(app.id, "secret", { name: "New", description: "" }),
      ).rejects.toThrow();
    }
    const api = createAPI(
      vi.fn<typeof fetch>().mockResolvedValue(response({ secrets: [valid] })),
    );
    expect((await api.listSecrets(app.id))[0].description).toBe("");
  });
  it("accepts stored BOM-prefixed names without altering the returned metadata", async () => {
    const metadata = {
      id: "secret",
      name: "\uFEFFProduction",
      description: "",
      created_at: app.created_at,
    };
    const api = createAPI(
      vi
        .fn<typeof fetch>()
        .mockResolvedValue(response({ secrets: [metadata] })),
    );
    expect(await api.listSecrets(app.id)).toEqual([metadata]);
  });
});

it("starts OAuth with only a provider and validates the authorization URL", async () => {
  const fetcher = vi
    .fn<typeof fetch>()
    .mockResolvedValueOnce(
      response({
        authorization_url:
          "https://console.example.test/auth/authorize?state=opaque",
      }),
    )
    .mockResolvedValueOnce(response({ csrf_token: "legacy" }));
  const api = createAPI(fetcher);
  expect(await api.login("google")).toEqual({
    authorization_url:
      "https://console.example.test/auth/authorize?state=opaque",
  });
  expect(fetcher).toHaveBeenCalledWith(
    "/console/api/login",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ provider: "google" }),
      headers: { "Content-Type": "application/json" },
    }),
  );
  await expect(api.login("github")).rejects.toThrow();
});

it("uses CSRF for explicit identity binding and encodes opaque user pagination cursors", async () => {
  const fetcher = vi
    .fn<typeof fetch>()
    .mockResolvedValueOnce(
      response({ csrf_token: "session-token", user: fixtureUser }),
    )
    .mockResolvedValueOnce(
      response({
        authorization_url: "https://console.example.test/auth/authorize",
      }),
    )
    .mockResolvedValueOnce(
      response({
        users: [
          {
            id: "usr_summary",
            email: null,
            name: null,
            created_at: app.created_at,
            app_count: 4,
            secrets: ["must-not-expose"],
          },
        ],
        next_cursor: null,
      }),
    );
  const api = createAPI(fetcher);
  await api.session();
  await api.linkIdentity("microsoft");
  expect(fetcher).toHaveBeenNthCalledWith(
    2,
    "/console/api/identities/link",
    expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ provider: "microsoft" }),
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": "session-token",
      },
    }),
  );
  const page = await api.listUsers("opaque/+==");
  expect(fetcher).toHaveBeenNthCalledWith(
    3,
    "/console/api/users?cursor=opaque%2F%2B%3D%3D",
    expect.objectContaining({ method: "GET" }),
  );
  expect(page.users[0]).toEqual({
    id: "usr_summary",
    email: null,
    name: null,
    created_at: app.created_at,
    app_count: 4,
  });
});

it("requires every account profile field and preserves nullable verification in linked identities", async () => {
  const identity = {
    provider: "microsoft",
    id: "microsoft:tenant:oid",
    email: null,
    name: null,
    email_verified: null,
    linked_at: app.created_at,
  };
  const fetcher = vi
    .fn<typeof fetch>()
    .mockResolvedValueOnce(
      response({
        csrf_token: "token",
        user: { ...fixtureUser, avatar_url: undefined },
      }),
    )
    .mockResolvedValueOnce(response({ identities: [identity] }))
    .mockResolvedValueOnce(
      response({ identities: [{ ...identity, email_verified: undefined }] }),
    )
    .mockResolvedValueOnce(response({ users: [], next_cursor: undefined }));
  const api = createAPI(fetcher);
  await expect(api.session()).rejects.toThrow();
  expect(await api.listIdentities()).toEqual([identity]);
  await expect(api.listIdentities()).rejects.toThrow();
  await expect(api.listUsers()).rejects.toThrow();
});
