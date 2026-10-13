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
import type { Application, ConsoleAPI, Secret } from "./api";

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
  listSecrets: vi
    .fn()
    .mockResolvedValue([
      { id: "sec_original", created_at: example.created_at },
    ]),
  createSecret: vi.fn().mockResolvedValue({
    id: "sec_rotated",
    secret: "rotated-only-once",
    created_at: example.created_at,
  }),
  resetSecret: vi.fn().mockResolvedValue({
    id: "sec_replacement",
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
        { id: "sec_original", created_at: example.created_at },
      ])
      .mockResolvedValueOnce([
        {
          id: "sec_original",
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

  it("requires confirmation, shows the replacement once, and owns the app until it is saved", async () => {
    const issued = {
      id: "sec_replacement",
      secret: "reset-only-once",
      created_at: example.created_at,
    };
    let rows: Secret[] = [
      { id: "sec_original", created_at: example.created_at },
    ];
    const api = apiFixture({
      listSecrets: vi.fn(async () => structuredClone(rows)),
      resetSecret: vi.fn(async () => {
        rows = [
          { ...rows[0], revoked_at: issued.created_at },
          { id: issued.id, created_at: issued.created_at },
        ];
        return issued;
      }),
    });
    render(<Console api={api} />);
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
        secret: "reset-only-once",
        created_at: example.created_at,
      });
    const api = apiFixture({ resetSecret });
    render(<Console api={api} />);
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
        created_at: example.created_at,
        revoked_at: example.created_at,
      },
      { id: "sec_replacement", created_at: example.created_at },
    ];
    const listSecrets = vi
      .fn()
      .mockResolvedValueOnce([
        { id: "sec_original", created_at: example.created_at },
      ])
      .mockResolvedValueOnce(rows);
    const resetSecret = vi
      .fn()
      .mockRejectedValue(
        new APIError(409, "already_revoked", "The secret is already revoked."),
      );
    const api = apiFixture({ listSecrets, resetSecret });
    render(<Console api={api} />);
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
        { id: "sec_original", created_at: example.created_at },
      ])
      .mockRejectedValueOnce(
        new APIError(
          401,
          "unauthorized",
          "Administrator authentication is required.",
        ),
      );
    const api = apiFixture({ listSecrets });
    render(<Console api={api} />);
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
    await screen.findByRole("button", { name: "Sign in" });
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
    expect(screen.getByRole("button", { name: "Sign in" })).toBeDefined();
  });
});
