import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { Console } from "./Console";
import { fixtureSession, completeAuthorization } from "./test-fixtures";
import { APIError } from "./api";
import type {
  Application,
  ConsoleAPI,
  CreatedApp,
  IssuedSecret,
  Secret,
} from "./api";

const example: Application = {
  id: "app_example",
  name: "Example",
  enabled: true,
  redirect_uris: ["https://example.test/callback"],
  providers: ["github"],
  created_at: "2026-10-12T09:00:00Z",
  updated_at: "2026-10-12T09:00:00Z",
};
const apiFixture = (overrides: Partial<ConsoleAPI> = {}): ConsoleAPI => ({
  session: vi.fn().mockResolvedValue(fixtureSession),
  login: vi.fn().mockResolvedValue({
    authorization_url: "https://oauth.example.test/authorize",
  }),
  listIdentities: vi.fn().mockResolvedValue([]),
  linkIdentity: vi.fn().mockResolvedValue({
    authorization_url: "https://oauth.example.test/link",
  }),
  listUsers: vi.fn().mockResolvedValue({ users: [], next_cursor: null }),
  logout: vi.fn().mockResolvedValue(undefined),
  listApps: vi.fn().mockResolvedValue([example]),
  listProviders: vi.fn().mockResolvedValue([
    { id: "microsoft", available: false },
    { id: "google", available: true },
    { id: "github", available: true },
  ]),
  createApp: vi.fn().mockResolvedValue({
    app: { ...example, id: "app_created", name: "Created" },
    secret: "only-visible-once",
    secret_id: "sec_created",
  }),
  updateApp: vi
    .fn()
    .mockImplementation(async (id, input) => ({ ...example, ...input, id })),
  deleteApp: vi.fn().mockResolvedValue(undefined),
  listSecrets: vi.fn().mockResolvedValue([
    {
      id: "sec_original",
      name: "Production",
      description: "Owner: Operations\nProduction service.",
      created_at: example.created_at,
    },
  ]),
  createSecret: vi.fn().mockResolvedValue({
    id: "sec_rotated",
    name: "Replacement",
    description: "",
    secret: "rotated-only-once",
    created_at: example.created_at,
  }),
  updateSecret: vi.fn().mockImplementation(async (_id, secretID, input) => ({
    id: secretID,
    ...input,
    created_at: example.created_at,
  })),
  resetSecret: vi.fn().mockResolvedValue({
    id: "sec_replacement",
    name: "Production",
    description: "Owner: Operations\nProduction service.",
    secret: "reset-only-once",
    created_at: example.created_at,
  }),
  revokeSecret: vi.fn().mockResolvedValue(undefined),
  ...overrides,
});

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute("open");
  };
});
afterEach(cleanup);

const openApp = async () => {
  fireEvent.click(
    await screen.findByRole("button", { name: "Manage Example" }),
  );
  await screen.findByText("sec_original");
};

const pendingIssuanceAfterUnrelatedRefresh = async (initialSecret: boolean) => {
  const other = { ...example, id: "app_other", name: "Other" };
  const created = { ...example, id: "app_created", name: "Created" };
  const applications = [example, other];
  const refresh = Promise.withResolvers<Application[]>();
  const creation = Promise.withResolvers<CreatedApp>();
  const issuance = Promise.withResolvers<IssuedSecret>();
  const issued: IssuedSecret = {
    id: "sec_pending",
    name: "Pending production",
    description: "",
    secret: "committed-only-once",
    created_at: example.created_at,
  };
  const api = apiFixture({
    listApps: vi
      .fn()
      .mockImplementation(async () => [...applications])
      .mockResolvedValueOnce([...applications])
      .mockRejectedValueOnce(new Error("Reconciliation unavailable."))
      .mockReturnValueOnce(refresh.promise),
    updateApp: vi.fn().mockRejectedValue(new Error("Write response lost.")),
    createApp: vi.fn().mockImplementation(() => {
      applications.push(created);
      return creation.promise;
    }),
    createSecret: vi.fn().mockReturnValue(issuance.promise),
  });
  render(<Console api={api} onAuthorization={completeAuthorization} />);
  await openApp();
  fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
  await screen.findByText("State unavailable");
  fireEvent.click(screen.getByRole("button", { name: "Refresh application" }));
  await waitFor(() => expect(api.listApps).toHaveBeenCalledTimes(3));
  fireEvent.click(screen.getByRole("button", { name: "← All applications" }));
  if (initialSecret) {
    fireEvent.click(screen.getByRole("button", { name: "New application" }));
    const editor = screen.getByRole("dialog", { name: "Create application" });
    fireEvent.change(within(editor).getByLabelText("Application name"), {
      target: { value: created.name },
    });
    fireEvent.change(within(editor).getByLabelText("Callback URLs"), {
      target: { value: created.redirect_uris[0] },
    });
    fireEvent.change(within(editor).getByLabelText("Secret name"), {
      target: { value: issued.name },
    });
    fireEvent.click(
      within(editor).getByRole("button", { name: "Create application" }),
    );
    await waitFor(() => expect(api.createApp).toHaveBeenCalledTimes(1));
  } else {
    fireEvent.click(screen.getByRole("button", { name: "Manage Other" }));
    await screen.findByText("sec_original");
    fireEvent.click(screen.getByRole("button", { name: "Create secret" }));
    const editor = screen.getByRole("dialog", { name: "Create secret" });
    fireEvent.change(within(editor).getByLabelText("Secret name"), {
      target: { value: issued.name },
    });
    fireEvent.click(
      within(editor).getByRole("button", { name: "Create secret" }),
    );
    await waitFor(() => expect(api.createSecret).toHaveBeenCalledTimes(1));
  }
  return {
    api,
    refresh,
    issued,
    creation,
    issuance,
    finish: () =>
      initialSecret
        ? creation.resolve({
            app: created,
            secret: issued.secret,
            secret_id: issued.id,
          })
        : issuance.resolve(issued),
    fail: (cause: Error) =>
      initialSecret ? creation.reject(cause) : issuance.reject(cause),
  };
};
const signInAgain = async () => {
  fireEvent.click(
    await screen.findByRole("button", { name: "Continue with Google" }),
  );
  await screen.findByRole("button", { name: "Sign out" });
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "New application" }) ??
        screen.queryByRole("button", { name: "← All applications" }),
    ).not.toBeNull(),
  );
};

describe("OAuth Console", () => {
  it("keeps a newly created secret visible until the member explicitly saves it", async () => {
    const api = apiFixture({
      listSecrets: vi
        .fn()
        .mockRejectedValue(new Error("Credential list unavailable.")),
    });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    fireEvent.click(
      await screen.findByRole("button", { name: "New application" }),
    );
    const editor = screen.getByRole("dialog", { name: "Create application" });
    fireEvent.change(within(editor).getByLabelText("Application name"), {
      target: { value: "Created" },
    });
    fireEvent.change(within(editor).getByLabelText("Callback URLs"), {
      target: { value: "https://created.test/callback" },
    });
    fireEvent.change(within(editor).getByLabelText("Secret name"), {
      target: { value: "Production" },
    });
    fireEvent.change(within(editor).getByLabelText("Description"), {
      target: { value: "  Deployment owner\nPlatform  " },
    });
    fireEvent.click(
      within(editor).getByRole("button", { name: "Create application" }),
    );
    const secret = await screen.findByRole("dialog", {
      name: "Save your app secret",
    });
    expect(within(secret).getByText("only-visible-once")).toBeDefined();
    expect(
      within(secret).queryByRole("button", { name: "Close dialog" }),
    ).toBeNull();
    expect(
      (
        within(secret).getByRole("button", {
          name: "Done",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    fireEvent(secret, new Event("cancel", { cancelable: true, bubbles: true }));
    expect(screen.getByText("only-visible-once")).toBeDefined();
    await screen.findByText("Credential list unavailable.");
    expect(screen.getByText("only-visible-once")).toBeDefined();
    fireEvent.click(
      within(secret).getByRole("checkbox", {
        name: "I have saved this secret securely.",
      }),
    );
    fireEvent.click(within(secret).getByRole("button", { name: "Done" }));
    await waitFor(() =>
      expect(screen.queryByText("only-visible-once")).toBeNull(),
    );
  });

  it("shows a failed mutation without pretending the application changed", async () => {
    const api = apiFixture({
      updateApp: vi
        .fn()
        .mockRejectedValue(
          new APIError(403, "invalid_csrf", "The CSRF token is invalid."),
        ),
    });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
    await screen.findByText("The CSRF token is invalid.");
    expect(screen.getByRole("button", { name: "Disable app" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Enable app" })).toBeNull();
  });

  it("reconciles an edited application and opens the next editor with saved configuration", async () => {
    const api = apiFixture();
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    fireEvent.click(screen.getByRole("button", { name: "Edit application" }));
    let editor = screen.getByRole("dialog", { name: "Edit application" });
    fireEvent.change(within(editor).getByLabelText("Application name"), {
      target: { value: "Renamed" },
    });
    fireEvent.change(within(editor).getByLabelText("Callback URLs"), {
      target: {
        value: "https://renamed.test/callback\nhttps://staging.test/callback",
      },
    });
    fireEvent.click(
      within(editor).getByRole("button", { name: "Save changes" }),
    );
    await screen.findByRole("heading", { name: "Renamed" });
    expect(screen.getByText("https://staging.test/callback")).toBeDefined();
    fireEvent.click(screen.getByRole("button", { name: "Edit application" }));
    editor = screen.getByRole("dialog", { name: "Edit application" });
    expect(
      (within(editor).getByLabelText("Application name") as HTMLInputElement)
        .value,
    ).toBe("Renamed");
    expect(
      (within(editor).getByLabelText("Callback URLs") as HTMLTextAreaElement)
        .value,
    ).toBe("https://renamed.test/callback\nhttps://staging.test/callback");
  });

  it("requires a new OAuth login when a mutation observes session expiry", async () => {
    const api = apiFixture({
      updateApp: vi
        .fn()
        .mockRejectedValue(
          new APIError(401, "unauthorized", "Authentication is required."),
        ),
    });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
    await screen.findByRole("button", { name: "Continue with Google" });
    expect(screen.queryByRole("button", { name: "Disable app" })).toBeNull();
    fireEvent.click(
      screen.getByRole("button", { name: "Continue with Google" }),
    );
    await openApp();
    expect(screen.getByRole("button", { name: "Disable app" })).toBeDefined();
    expect(api.login).toHaveBeenCalledWith("google");
    expect(api.listApps).toHaveBeenCalledTimes(2);
  });

  it("revokes credentials only after a confirmed action and reloads their current status", async () => {
    const listSecrets = vi
      .fn()
      .mockResolvedValueOnce([
        {
          id: "sec_original",
          name: "Production",
          description: "Owner: Operations\nProduction service.",
          created_at: example.created_at,
        },
      ])
      .mockResolvedValueOnce([
        {
          id: "sec_original",
          name: "Production",
          description: "Owner: Operations\nProduction service.",
          created_at: example.created_at,
          revoked_at: example.created_at,
        },
      ]);
    const api = apiFixture({ listSecrets });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    fireEvent.click(
      screen.getByRole("button", { name: "Revoke secret sec_original" }),
    );
    expect(api.revokeSecret).not.toHaveBeenCalled();
    const dialog = screen.getByRole("dialog", { name: "Revoke this secret?" });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Revoke secret" }),
    );
    await screen.findByText("Revoked");
    expect(api.revokeSecret).toHaveBeenCalledWith(example.id, "sec_original");
    expect(
      screen.queryByRole("button", { name: "Revoke secret sec_original" }),
    ).toBeNull();
  });
  it("edits active and revoked metadata without issuing a credential and clears descriptions verbatim", async () => {
    let secrets: Secret[] = [
      {
        id: "sec_original",
        name: "Production",
        description: "  Owner: Operations\nKeep these spaces.  ",
        created_at: example.created_at,
      },
      {
        id: "sec_retired",
        name: "Retired",
        description: "Old deployment",
        created_at: example.created_at,
        revoked_at: example.updated_at,
      },
    ];
    const api = apiFixture({
      listSecrets: vi
        .fn()
        .mockImplementation(async () => structuredClone(secrets)),
      updateSecret: vi.fn().mockImplementation(async (_appID, id, input) => {
        const original = secrets.find((secret) => secret.id === id);
        if (original === undefined) throw new Error("Missing fixture secret");
        const updated = { ...original, ...input };
        secrets = secrets.map((secret) =>
          secret.id === id ? updated : secret,
        );
        return updated;
      }),
    });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    fireEvent.click(
      screen.getByRole("button", {
        name: "Edit details for secret sec_original",
      }),
    );
    let editor = screen.getByRole("dialog", { name: "Edit secret details" });
    expect(
      (within(editor).getByLabelText("Description") as HTMLTextAreaElement)
        .value,
    ).toBe(secrets[0].description);
    fireEvent.change(within(editor).getByLabelText("Secret name"), {
      target: { value: "Production API" },
    });
    fireEvent.change(within(editor).getByLabelText("Description"), {
      target: { value: "" },
    });
    fireEvent.click(
      within(editor).getByRole("button", { name: "Save details" }),
    );
    await screen.findByText("Production API");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Edit details for secret sec_retired",
      }),
    );
    editor = screen.getByRole("dialog", { name: "Edit secret details" });
    fireEvent.change(within(editor).getByLabelText("Secret name"), {
      target: { value: "Retired production" },
    });
    fireEvent.change(within(editor).getByLabelText("Description"), {
      target: { value: "  Replaced\nOwner: 开发团队  " },
    });
    fireEvent.click(
      within(editor).getByRole("button", { name: "Save details" }),
    );
    await screen.findByText("Retired production");
    expect(secrets[0].description).toBe("");
    expect(secrets[1]).toMatchObject({
      revoked_at: example.updated_at,
      description: "  Replaced\nOwner: 开发团队  ",
    });
    expect(api.createSecret).not.toHaveBeenCalled();
    expect(api.revokeSecret).not.toHaveBeenCalled();
    expect(
      screen.queryByRole("dialog", { name: "Save your app secret" }),
    ).toBeNull();
    expect(api.updateSecret).toHaveBeenCalledWith(example.id, "sec_original", {
      name: "Production API",
      description: "",
    });
  });

  it("retains metadata drafts and errors after a failed save and releases the App only on cancellation", async () => {
    const api = apiFixture({
      updateSecret: vi
        .fn()
        .mockRejectedValue(new Error("Metadata write failed.")),
    });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    fireEvent.click(
      screen.getByRole("button", {
        name: "Edit details for secret sec_original",
      }),
    );
    const editor = screen.getByRole("dialog", { name: "Edit secret details" });
    fireEvent.change(within(editor).getByLabelText("Secret name"), {
      target: { value: "Draft production" },
    });
    fireEvent.click(
      within(editor).getByRole("button", { name: "Save details" }),
    );
    await within(editor).findByText("Metadata write failed.");
    expect(
      (within(editor).getByLabelText("Secret name") as HTMLInputElement).value,
    ).toBe("Draft production");
    expect(
      (screen.getByRole("button", { name: "Disable app" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    fireEvent.click(within(editor).getByRole("button", { name: "Cancel" }));
    expect(
      (screen.getByRole("button", { name: "Disable app" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
  });

  it("owns Secret creation synchronously through the one-time acknowledgement despite duplicate events and session expiry", async () => {
    let resolveIssue!: (issued: IssuedSecret) => void;
    const issuance = new Promise<IssuedSecret>((resolve) => {
      resolveIssue = resolve;
    });
    const api = apiFixture({
      createSecret: vi.fn().mockReturnValue(issuance),
      listSecrets: vi
        .fn()
        .mockResolvedValueOnce([
          {
            id: "sec_original",
            name: "Production",
            description: "",
            created_at: example.created_at,
          },
        ])
        .mockRejectedValue(
          new APIError(401, "unauthorized", "Session expired after issuance."),
        ),
    });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    const create = screen.getByRole("button", { name: "Create secret" });
    const remove = screen.getByRole("button", { name: "Delete application" });
    const logout = screen.getByRole("button", { name: "Sign out" });
    const navigation = screen.getByRole("button", {
      name: "← All applications",
    });
    act(() => {
      create.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      remove.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      logout.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      navigation.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    const editor = screen.getByRole("dialog", { name: "Create secret" });
    expect(
      screen.queryByRole("dialog", { name: "Delete application?" }),
    ).toBeNull();
    fireEvent.change(within(editor).getByLabelText("Secret name"), {
      target: { value: "Staging backend" },
    });
    fireEvent.change(within(editor).getByLabelText("Description"), {
      target: { value: "  Owner\nStaging  " },
    });
    const submit = within(editor).getByRole("button", {
      name: "Create secret",
    });
    act(() => {
      submit.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      submit.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      remove.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      logout.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      navigation.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(api.createSecret).toHaveBeenCalledTimes(1);
    expect(api.deleteApp).not.toHaveBeenCalled();
    expect(api.logout).not.toHaveBeenCalled();
    await act(async () =>
      resolveIssue({
        id: "sec_new",
        name: "Staging backend",
        description: "  Owner\nStaging  ",
        secret: "never-discard-this-value",
        created_at: example.created_at,
      }),
    );
    const credential = await screen.findByRole("dialog", {
      name: "Save your app secret",
    });
    expect(
      within(credential).getByText("never-discard-this-value"),
    ).toBeDefined();
    expect(within(credential).getByText("Staging backend")).toBeDefined();
    await screen.findByRole("button", { name: "Continue with Google" });
    expect(
      within(credential).getByText("never-discard-this-value"),
    ).toBeDefined();
    fireEvent(
      credential,
      new Event("cancel", { cancelable: true, bubbles: true }),
    );
    expect(screen.getByText("never-discard-this-value")).toBeDefined();
    expect(
      (
        within(credential).getByRole("button", {
          name: "Done",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    fireEvent.click(within(credential).getByRole("checkbox"));
    fireEvent.click(within(credential).getByRole("button", { name: "Done" }));
    expect(screen.queryByText("never-discard-this-value")).toBeNull();
  });

  it("validates UTF-8 metadata limits without changing the draft or issuing a secret", async () => {
    const api = apiFixture();
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    fireEvent.click(screen.getByRole("button", { name: "Create secret" }));
    const editor = screen.getByRole("dialog", { name: "Create secret" });
    for (const name of [" Surrounding ", "🔑".repeat(33), "\u0085Production"]) {
      fireEvent.change(within(editor).getByLabelText("Secret name"), {
        target: { value: name },
      });
      fireEvent.submit(
        within(editor)
          .getByRole("button", { name: "Create secret" })
          .closest("form")!,
      );
      expect(within(editor).getByRole("alert").textContent).toContain(
        "UTF-8 bytes",
      );
    }
    fireEvent.change(within(editor).getByLabelText("Secret name"), {
      target: { value: "Valid" },
    });
    fireEvent.change(within(editor).getByLabelText("Description"), {
      target: { value: "🔑".repeat(1025) },
    });
    fireEvent.click(
      within(editor).getByRole("button", { name: "Create secret" }),
    );
    expect(within(editor).getByRole("alert").textContent).toContain(
      "4096 UTF-8 bytes",
    );
    expect(api.createSecret).not.toHaveBeenCalled();
  });
  it("serializes an in-flight metadata edit against revocation and App deletion", async () => {
    const original: Secret = {
      id: "sec_original",
      name: "Production",
      description: "",
      created_at: example.created_at,
    };
    let persisted = original;
    let resolveUpdate!: (secret: Secret) => void;
    const update = new Promise<Secret>((resolve) => {
      resolveUpdate = resolve;
    });
    const api = apiFixture({
      listSecrets: vi.fn().mockImplementation(async () => [persisted]),
      updateSecret: vi.fn().mockImplementation((_appID, _secretID, input) => {
        persisted = { ...original, ...input };
        return update;
      }),
    });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    const revoke = screen.getByRole("button", {
      name: "Revoke secret sec_original",
    });
    const remove = screen.getByRole("button", { name: "Delete application" });
    fireEvent.click(
      screen.getByRole("button", {
        name: "Edit details for secret sec_original",
      }),
    );
    const editor = screen.getByRole("dialog", { name: "Edit secret details" });
    fireEvent.change(within(editor).getByLabelText("Secret name"), {
      target: { value: "Production API" },
    });
    const save = within(editor).getByRole("button", { name: "Save details" });
    act(() => {
      save.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      save.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      revoke.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      remove.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    expect(api.updateSecret).toHaveBeenCalledTimes(1);
    expect(api.createSecret).not.toHaveBeenCalled();
    expect(api.revokeSecret).not.toHaveBeenCalled();
    expect(
      screen.queryByRole("dialog", { name: "Revoke this secret?" }),
    ).toBeNull();
    expect(
      screen.queryByRole("dialog", { name: "Delete application?" }),
    ).toBeNull();
    await act(async () => resolveUpdate(persisted));
    await screen.findByText("Production API");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(
      (
        screen.getByRole("button", {
          name: "Delete application",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });

  it("ignores a stale credential listing after an issuance begins", async () => {
    let resolveListing!: (secrets: Secret[]) => void;
    const listing = new Promise<Secret[]>((resolve) => {
      resolveListing = resolve;
    });
    let resolveIssue!: (issued: IssuedSecret) => void;
    const issuance = new Promise<IssuedSecret>((resolve) => {
      resolveIssue = resolve;
    });
    const issued = {
      id: "sec_current",
      name: "Current",
      description: "",
      secret: "latest-only-once",
      created_at: example.created_at,
    };
    const api = apiFixture({
      listSecrets: vi
        .fn()
        .mockReturnValueOnce(listing)
        .mockResolvedValue([issued]),
      createSecret: vi.fn().mockReturnValue(issuance),
    });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Manage Example" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Create secret" }),
    );
    const editor = screen.getByRole("dialog", { name: "Create secret" });
    fireEvent.change(within(editor).getByLabelText("Secret name"), {
      target: { value: "Current" },
    });
    fireEvent.click(
      within(editor).getByRole("button", { name: "Create secret" }),
    );
    await act(async () =>
      resolveListing([
        {
          id: "sec_stale",
          name: "Stale",
          description: "",
          created_at: example.created_at,
        },
      ]),
    );
    expect(screen.queryByText("sec_stale")).toBeNull();
    await act(async () => resolveIssue(issued));
    const credential = await screen.findByRole("dialog", {
      name: "Save your app secret",
    });
    expect(within(credential).getByText("latest-only-once")).toBeDefined();
    fireEvent.click(within(credential).getByRole("checkbox"));
    fireEvent.click(within(credential).getByRole("button", { name: "Done" }));
    await screen.findByText("sec_current");
    expect(screen.queryByText("sec_stale")).toBeNull();
  });
  it.each([
    { initialSecret: true, earlyLogin: false },
    { initialSecret: false, earlyLogin: false },
    { initialSecret: true, earlyLogin: true },
    { initialSecret: false, earlyLogin: true },
  ])(
    "preserves an issued value through an unrelated401 and acknowledgement (initial=$initialSecret, early login=$earlyLogin)",
    async ({ initialSecret, earlyLogin }) => {
      const operation =
        await pendingIssuanceAfterUnrelatedRefresh(initialSecret);
      await act(async () =>
        operation.refresh.reject(
          new APIError(
            401,
            "unauthorized",
            "Unrelated refresh requires authentication.",
          ),
        ),
      );
      await screen.findByRole("button", { name: "Continue with Google" });
      expect(screen.queryByRole("dialog")).toBeNull();
      const login = screen.getByRole("button", {
        name: "Continue with Google",
      });
      expect((login as HTMLButtonElement).disabled).toBe(true);
      if (earlyLogin) {
        fireEvent.click(login);
        expect(operation.api.login).not.toHaveBeenCalled();
      }
      await act(async () => operation.finish());
      const credential = await screen.findByRole("dialog", {
        name: "Save your app secret",
      });
      expect(
        within(credential).getByText(operation.issued.secret),
      ).toBeDefined();
      fireEvent.click(
        within(credential).getByRole("button", { name: "Copy App secret" }),
      );
      await within(credential).findByText(
        "Clipboard unavailable. Select and copy the value above.",
      );
      expect(
        (
          within(credential).getByRole("button", {
            name: "Done",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(true);
      const blockedLogin = screen.getByRole("button", {
        name: "Continue with Google",
      });
      expect((blockedLogin as HTMLButtonElement).disabled).toBe(true);
      act(() => {
        blockedLogin.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      });
      expect(operation.api.login).not.toHaveBeenCalled();
      fireEvent.click(
        within(credential).getByRole("checkbox", {
          name: "I have saved this secret securely.",
        }),
      );
      fireEvent.click(within(credential).getByRole("button", { name: "Done" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(screen.queryByText(operation.issued.secret)).toBeNull();
      expect(
        (
          screen.getByRole("button", {
            name: "Continue with Google",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false);
      await signInAgain();
      const back = screen.queryByRole("button", { name: "← All applications" });
      if (back !== null) fireEvent.click(back);
      fireEvent.click(screen.getByRole("button", { name: "New application" }));
      const editor = screen.getByRole("dialog", { name: "Create application" });
      fireEvent.click(within(editor).getByRole("button", { name: "Cancel" }));
      expect(
        (screen.getByRole("button", { name: "Sign out" }) as HTMLButtonElement)
          .disabled,
      ).toBe(false);
      expect(operation.api.createApp).toHaveBeenCalledTimes(
        initialSecret ? 1 : 0,
      );
      expect(operation.api.createSecret).toHaveBeenCalledTimes(
        initialSecret ? 0 : 1,
      );
    },
  );

  it.each([true, false])(
    "releases detached issuance ownership after a request failure (initial=%s)",
    async (initialSecret) => {
      const operation =
        await pendingIssuanceAfterUnrelatedRefresh(initialSecret);
      await act(async () =>
        operation.refresh.reject(
          new APIError(
            401,
            "unauthorized",
            "Unrelated refresh requires authentication.",
          ),
        ),
      );
      await screen.findByRole("button", { name: "Continue with Google" });
      await act(async () =>
        operation.fail(new Error("Issuance response lost.")),
      );
      expect(screen.queryByRole("dialog")).toBeNull();
      await signInAgain();
      const back = screen.queryByRole("button", { name: "← All applications" });
      if (back !== null) fireEvent.click(back);
      fireEvent.click(screen.getByRole("button", { name: "New application" }));
      const editor = screen.getByRole("dialog", { name: "Create application" });
      fireEvent.click(within(editor).getByRole("button", { name: "Cancel" }));
      fireEvent.click(screen.getByRole("button", { name: "Manage Other" }));
      await screen.findByText("sec_original");
      fireEvent.click(screen.getByRole("button", { name: "Create secret" }));
      const secretEditor = screen.getByRole("dialog", {
        name: "Create secret",
      });
      fireEvent.click(
        within(secretEditor).getByRole("button", { name: "Cancel" }),
      );
      expect(
        (screen.getByRole("button", { name: "Sign out" }) as HTMLButtonElement)
          .disabled,
      ).toBe(false);
      expect(operation.api.createApp).toHaveBeenCalledTimes(
        initialSecret ? 1 : 0,
      );
      expect(operation.api.createSecret).toHaveBeenCalledTimes(
        initialSecret ? 0 : 1,
      );
    },
  );
  it("requires confirmation, shows the replacement once, and owns the app until it is saved", async () => {
    const issued = {
      id: "sec_replacement",
      name: "Production",
      description: "Owner: Operations\nProduction service.",
      secret: "reset-only-once",
      created_at: example.created_at,
    };
    let rows: Secret[] = [
      {
        id: "sec_original",
        name: "Production",
        description: "Owner: Operations\nProduction service.",
        created_at: example.created_at,
      },
    ];
    const api = apiFixture({
      listSecrets: vi.fn(async () => structuredClone(rows)),
      resetSecret: vi.fn(async () => {
        rows = [
          { ...rows[0], revoked_at: issued.created_at },
          {
            id: issued.id,
            name: issued.name,
            description: issued.description,
            created_at: issued.created_at,
          },
        ];
        return issued;
      }),
    });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();

    act(() => {
      fireEvent.click(
        screen.getByRole("button", { name: "Reset secret sec_original" }),
      );
      fireEvent.click(screen.getByRole("button", { name: "Create secret" }));
      fireEvent.click(
        screen.getByRole("button", { name: "Revoke secret sec_original" }),
      );
      fireEvent.click(
        screen.getByRole("button", { name: "← All applications" }),
      );
      fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
    });
    let confirmation = screen.getByRole("dialog", {
      name: "Reset this secret?",
    });
    expect(api.createSecret).not.toHaveBeenCalled();
    expect(api.revokeSecret).not.toHaveBeenCalled();
    expect(api.logout).not.toHaveBeenCalled();
    expect(confirmation.textContent).toContain("sec_original");
    expect(confirmation.textContent).toContain("Production");
    expect(confirmation.textContent).toContain("stop working immediately");
    expect(confirmation.textContent).toContain("shown once");
    expect(confirmation.textContent).toContain("cannot be recovered");
    expect(api.resetSecret).not.toHaveBeenCalled();
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Cancel" }),
    );
    expect(api.resetSecret).not.toHaveBeenCalled();
    expect(
      screen.getByRole("button", { name: "Reset secret sec_original" }),
    ).toBeDefined();

    fireEvent.click(
      screen.getByRole("button", { name: "Reset secret sec_original" }),
    );
    confirmation = screen.getByRole("dialog", { name: "Reset this secret?" });
    expect(
      (
        screen.getByRole("button", {
          name: "← All applications",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(
      (screen.getByRole("button", { name: "Sign out" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Reset secret" }),
    );
    const credential = await screen.findByRole("dialog", {
      name: "Save your app secret",
    });
    expect(api.resetSecret).toHaveBeenCalledExactlyOnceWith(
      example.id,
      "sec_original",
    );
    expect(within(credential).getByText("reset-only-once")).toBeDefined();
    expect(within(credential).getByText("Production")).toBeDefined();
    expect(
      within(credential).getByText("Owner: Operations Production service."),
    ).toBeDefined();
    expect(credential.textContent).toContain("sec_original has been revoked");
    expect(
      (screen.getByRole("button", { name: "Sign out" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(
      (
        within(credential).getByRole("button", {
          name: "Done",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    fireEvent.click(within(credential).getByRole("checkbox"));
    fireEvent.click(within(credential).getByRole("button", { name: "Done" }));
    await waitFor(() =>
      expect(screen.queryByText("reset-only-once")).toBeNull(),
    );
    await screen.findByText("Revoked");
    expect(
      (screen.getByRole("button", { name: "Sign out" }) as HTMLButtonElement)
        .disabled,
    ).toBe(false);
    expect(
      screen.getByRole("button", { name: "Reset secret sec_replacement" }),
    ).toBeDefined();
  });

  it("keeps the confirmation available for retry when the original secret is still active", async () => {
    const resetSecret = vi
      .fn()
      .mockRejectedValueOnce(new Error("Connection interrupted."))
      .mockResolvedValueOnce({
        id: "sec_replacement",
        name: "Production",
        description: "Owner: Operations\nProduction service.",
        secret: "reset-only-once",
        created_at: example.created_at,
      });
    const api = apiFixture({ resetSecret });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    fireEvent.click(
      screen.getByRole("button", { name: "Reset secret sec_original" }),
    );
    const confirmation = screen.getByRole("dialog", {
      name: "Reset this secret?",
    });
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Reset secret" }),
    );
    await within(confirmation).findByText("Connection interrupted.");
    expect(
      (
        within(confirmation).getByRole("button", {
          name: "Reset secret",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Reset secret" }),
    );
    await screen.findByText("reset-only-once");
    expect(resetSecret).toHaveBeenCalledTimes(2);
  });

  it("reports an already revoked secret when the new plaintext was lost", async () => {
    const rows: Secret[] = [
      {
        id: "sec_original",
        name: "Production",
        description: "Owner: Operations\nProduction service.",
        created_at: example.created_at,
        revoked_at: example.created_at,
      },
      {
        id: "sec_replacement",
        name: "Production",
        description: "Owner: Operations\nProduction service.",
        created_at: example.created_at,
      },
    ];
    const listSecrets = vi
      .fn()
      .mockResolvedValueOnce([
        {
          id: "sec_original",
          name: "Production",
          description: "Owner: Operations\nProduction service.",
          created_at: example.created_at,
        },
      ])
      .mockResolvedValueOnce(rows);
    const resetSecret = vi
      .fn()
      .mockRejectedValue(
        new APIError(409, "already_revoked", "The secret is already revoked."),
      );
    const api = apiFixture({ listSecrets, resetSecret });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    fireEvent.click(
      screen.getByRole("button", { name: "Reset secret sec_original" }),
    );
    const confirmation = screen.getByRole("dialog", {
      name: "Reset this secret?",
    });
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Reset secret" }),
    );
    await within(confirmation).findByText(/new key was not received/);
    expect(confirmation.textContent).toContain("Create another secret");
    expect(
      (
        within(confirmation).getByRole("button", {
          name: "Reset secret",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Close" }),
    );
    expect(resetSecret).toHaveBeenCalledTimes(1);
    expect(screen.getByText("sec_replacement")).toBeDefined();
    expect(screen.getByText("Revoked")).toBeDefined();
    expect(screen.queryByText("reset-only-once")).toBeNull();
  });

  it("preserves the one-time key when session expiry interrupts the post-reset list refresh", async () => {
    const listSecrets = vi
      .fn()
      .mockResolvedValueOnce([
        {
          id: "sec_original",
          name: "Production",
          description: "Owner: Operations\nProduction service.",
          created_at: example.created_at,
        },
      ])
      .mockRejectedValueOnce(
        new APIError(401, "unauthorized", "Authentication is required."),
      );
    const api = apiFixture({ listSecrets });
    render(<Console api={api} onAuthorization={completeAuthorization} />);
    await openApp();
    fireEvent.click(
      screen.getByRole("button", { name: "Reset secret sec_original" }),
    );
    const confirmation = screen.getByRole("dialog", {
      name: "Reset this secret?",
    });
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Reset secret" }),
    );
    const credential = await screen.findByRole("dialog", {
      name: "Save your app secret",
    });
    await screen.findByRole("button", { name: "Continue with Google" });
    expect(within(credential).getByText("reset-only-once")).toBeDefined();
    expect(
      (
        within(credential).getByRole("button", {
          name: "Done",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    fireEvent.click(within(credential).getByRole("checkbox"));
    fireEvent.click(within(credential).getByRole("button", { name: "Done" }));
    await waitFor(() =>
      expect(screen.queryByText("reset-only-once")).toBeNull(),
    );
    expect(
      screen.getByRole("button", { name: "Continue with Google" }),
    ).toBeDefined();
  });
  it.each([true, false])(
    "settles reset ownership after an unrelated session expiry (success=%s)",
    async (succeeds) => {
      const other = { ...example, id: "app_other", name: "Other" };
      const refresh = Promise.withResolvers<Application[]>();
      const reset = Promise.withResolvers<IssuedSecret>();
      const api = apiFixture({
        listApps: vi
          .fn()
          .mockResolvedValue([example, other])
          .mockResolvedValueOnce([example, other])
          .mockRejectedValueOnce(new Error("Reconciliation unavailable."))
          .mockReturnValueOnce(refresh.promise),
        updateApp: vi.fn().mockRejectedValue(new Error("Write response lost.")),
        resetSecret: vi.fn().mockReturnValue(reset.promise),
      });
      render(<Console api={api} onAuthorization={completeAuthorization} />);
      await openApp();
      fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
      await screen.findByText("State unavailable");
      fireEvent.click(
        screen.getByRole("button", { name: "Refresh application" }),
      );
      await waitFor(() => expect(api.listApps).toHaveBeenCalledTimes(3));
      fireEvent.click(
        screen.getByRole("button", { name: "← All applications" }),
      );
      fireEvent.click(screen.getByRole("button", { name: "Manage Other" }));
      await screen.findByText("sec_original");
      fireEvent.click(
        screen.getByRole("button", { name: "Reset secret sec_original" }),
      );
      const confirmation = screen.getByRole("dialog", {
        name: "Reset this secret?",
      });
      fireEvent.click(
        within(confirmation).getByRole("button", { name: "Reset secret" }),
      );
      await act(async () =>
        refresh.reject(new APIError(401, "unauthorized", "Session expired.")),
      );
      await screen.findByRole("button", { name: "Continue with Google" });
      expect(screen.queryByRole("dialog")).toBeNull();
      if (succeeds) {
        await act(async () =>
          reset.resolve({
            id: "sec_replacement",
            name: "Production",
            description: "",
            secret: "delayed-reset-key",
            created_at: example.created_at,
          }),
        );
        const credential = await screen.findByRole("dialog", {
          name: "Save your app secret",
        });
        expect(within(credential).getByText("delayed-reset-key")).toBeDefined();
        fireEvent.click(within(credential).getByRole("checkbox"));
        fireEvent.click(
          within(credential).getByRole("button", { name: "Done" }),
        );
      } else {
        await act(async () => reset.reject(new Error("Reset response lost.")));
      }
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      await signInAgain();
      expect(
        (screen.getByRole("button", { name: "Sign out" }) as HTMLButtonElement)
          .disabled,
      ).toBe(false);
      expect(api.resetSecret).toHaveBeenCalledExactlyOnceWith(
        other.id,
        "sec_original",
      );
    },
  );
});
