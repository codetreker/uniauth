import { describe, expect, it, vi } from "vitest";
import { APIError, createAPI } from "./api";

const app = {
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
  it("sends secret metadata with CSRF and encoded application and secret IDs", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ csrf_token: "session-token" }))
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

  it("adopts a new session after authentication and clears it on logout", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ csrf_token: "first-token" }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(response({ csrf_token: "new-token" }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    const api = createAPI(fetcher);
    await api.login("first-password");
    await api.logout();
    await api.login("new-password");
    await api.deleteApp(app.id);
    const loginOptions = fetcher.mock.calls[2][1];
    expect(loginOptions?.headers).toEqual({
      "Content-Type": "application/json",
    });
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
