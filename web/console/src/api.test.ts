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
  it("uses the existing session CSRF token for empty-body secret mutations", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ csrf_token: "session-token" }))
      .mockResolvedValueOnce(
        response(
          { id: "secret", secret: "plaintext", created_at: app.created_at },
          201,
        ),
      );
    const api = createAPI(fetcher);
    await api.session();
    await api.createSecret("app/with/slash");
    expect(fetcher).toHaveBeenLastCalledWith(
      "/console/api/apps/app%2Fwith%2Fslash/secrets",
      expect.objectContaining({
        method: "POST",
        headers: { "X-CSRF-Token": "session-token" },
        body: undefined,
        credentials: "same-origin",
      }),
    );
  });

  it("resets the encoded secret ID with a bodyless POST and validates the new credential", async () => {
    const issued = {
      id: "sec_replacement",
      secret: "new-plaintext",
      created_at: app.created_at,
    };
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ csrf_token: "session-token" }))
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
});
