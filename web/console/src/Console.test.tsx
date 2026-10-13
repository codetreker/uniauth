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
import { APIError } from "./api";
import type { Application, ConsoleAPI, IssuedSecret, Secret } from "./api";

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
  session: vi.fn().mockResolvedValue({ csrf_token: "token" }),
  login: vi.fn().mockResolvedValue({ csrf_token: "token" }),
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

describe("Administrator Console", () => {
  it("keeps a newly created secret visible until the administrator explicitly saves it", async () => {
    const api = apiFixture({
      listSecrets: vi
        .fn()
        .mockRejectedValue(new Error("Credential list unavailable.")),
    });
    render(<Console api={api} />);
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
    render(<Console api={api} />);
    await openApp();
    fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
    await screen.findByText("The CSRF token is invalid.");
    expect(screen.getByRole("button", { name: "Disable app" })).toBeDefined();
    expect(screen.queryByRole("button", { name: "Enable app" })).toBeNull();
  });

  it("reconciles an edited application and opens the next editor with saved configuration", async () => {
    const api = apiFixture();
    render(<Console api={api} />);
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

  it("requires a new administrator login when a mutation observes session expiry", async () => {
    const api = apiFixture({
      updateApp: vi
        .fn()
        .mockRejectedValue(
          new APIError(
            401,
            "unauthorized",
            "Administrator authentication is required.",
          ),
        ),
    });
    render(<Console api={api} />);
    await openApp();
    fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
    await screen.findByRole("button", { name: "Sign in" });
    expect(screen.queryByRole("button", { name: "Disable app" })).toBeNull();
    fireEvent.change(screen.getByLabelText("Administrator password"), {
      target: { value: "password" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await screen.findByRole("button", { name: "Disable app" });
    expect(api.login).toHaveBeenCalledWith("password");
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
    render(<Console api={api} />);
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
    render(<Console api={api} />);
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
    render(<Console api={api} />);
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
    render(<Console api={api} />);
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
    await screen.findByLabelText("Administrator password");
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
    render(<Console api={api} />);
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
    render(<Console api={api} />);
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
    render(<Console api={api} />);
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
});
