import { afterEach, expect, it, vi } from "vitest";
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
import type { ConsoleAPI, LinkedIdentity, Provider, UserPage } from "./api";
import { createMockAPI } from "./mock-api";
import {
  completeAuthorization,
  fixtureSession,
  fixtureUser,
} from "./test-fixtures";

const providers: Provider[] = [
  { id: "microsoft", available: true },
  { id: "google", available: true },
  { id: "github", available: true },
];
const identity: LinkedIdentity = {
  provider: "microsoft",
  id: "microsoft:tenant:stable-id",
  email: null,
  email_verified: null,
  name: null,
  linked_at: fixtureUser.created_at,
};
const fixture = (overrides: Partial<ConsoleAPI> = {}): ConsoleAPI => ({
  ...createMockAPI(),
  session: vi.fn().mockResolvedValue(fixtureSession),
  listApps: vi.fn().mockResolvedValue([]),
  listProviders: vi.fn().mockResolvedValue(providers),
  listIdentities: vi.fn().mockResolvedValue([identity]),
  login: vi.fn().mockResolvedValue({
    authorization_url: "https://oauth.example.test/login",
  }),
  linkIdentity: vi.fn().mockResolvedValue({
    authorization_url: "https://oauth.example.test/link",
  }),
  listUsers: vi.fn().mockResolvedValue({ users: [], next_cursor: null }),
  ...overrides,
});
afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
});

it("shows every configured OAuth method and completes only the chosen provider without a password form", async () => {
  const api = fixture({
    session: vi
      .fn()
      .mockRejectedValue(
        new APIError(401, "unauthorized", "Sign in required."),
      ),
    listProviders: vi.fn().mockResolvedValue([
      { id: "microsoft", available: true },
      { id: "google", available: false },
      { id: "github", available: true },
    ]),
  });
  const navigation = vi.fn().mockResolvedValue(fixtureSession);
  render(<Console api={api} onAuthorization={navigation} />);
  const microsoft = await screen.findByRole("button", {
    name: "Continue with Microsoft",
  });
  expect(
    screen.getByRole("button", { name: "Continue with GitHub" }),
  ).toBeDefined();
  expect(
    screen.queryByRole("button", { name: "Continue with Google" }),
  ).toBeNull();
  expect(document.querySelector('input[type="password"]')).toBeNull();
  fireEvent.click(microsoft);
  await screen.findByRole("button", { name: "Sign out" });
  expect(api.login).toHaveBeenCalledWith("microsoft");
  expect(navigation).toHaveBeenCalledWith("https://oauth.example.test/login");
});

it("makes provider loading, retry and no configured providers explicit before login", async () => {
  const pending = Promise.withResolvers<Provider[]>();
  const api = fixture({
    session: vi
      .fn()
      .mockRejectedValue(
        new APIError(401, "unauthorized", "Sign in required."),
      ),
    listProviders: vi
      .fn()
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValue([]),
  });
  render(<Console api={api} />);
  await screen.findByText("Loading sign-in methods…");
  expect(api.login).not.toHaveBeenCalled();
  await act(async () =>
    pending.reject(new Error("Provider registry unavailable.")),
  );
  await screen.findByText("Provider registry unavailable.");
  fireEvent.click(
    screen.getByRole("button", { name: "Retry sign-in methods" }),
  );
  await screen.findByText(
    "No sign-in providers are configured. Contact the platform operator.",
  );
  expect(screen.queryByRole("button", { name: /Continue with/ })).toBeNull();
});

it("retains login failure and prevents synchronous duplicate OAuth requests", async () => {
  const pending = Promise.withResolvers<{ authorization_url: string }>();
  const api = fixture({
    session: vi
      .fn()
      .mockRejectedValue(
        new APIError(401, "unauthorized", "Sign in required."),
      ),
    login: vi.fn().mockReturnValue(pending.promise),
  });
  render(<Console api={api} onAuthorization={completeAuthorization} />);
  const google = await screen.findByRole("button", {
    name: "Continue with Google",
  });
  const github = screen.getByRole("button", { name: "Continue with GitHub" });
  act(() => {
    google.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    github.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  expect(api.login).toHaveBeenCalledTimes(1);
  await act(async () =>
    pending.reject(new APIError(503, "unavailable", "Please try later.")),
  );
  await screen.findByText("Please try later.");
  expect(
    (
      screen.getByRole("button", {
        name: "Continue with Google",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
});

it("shows explicit nullable account fields and stable linked identity IDs", async () => {
  const api = fixture({
    session: vi.fn().mockResolvedValue({
      ...fixtureSession,
      user: { ...fixtureUser, name: null, email: null, avatar_url: null },
    }),
  });
  render(<Console api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: "My account" }));
  await screen.findByText(identity.id);
  expect(screen.getAllByText("Unnamed account").length).toBeGreaterThan(0);
  expect(screen.getAllByText("Email not provided").length).toBeGreaterThan(0);
  expect(screen.getByText("Verification not provided")).toBeDefined();
  expect(screen.getByText(fixtureUser.id)).toBeDefined();
  expect(
    (
      screen.getByRole("button", {
        name: "Microsoft linked",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(screen.queryByRole("button", { name: "Users" })).toBeNull();
  expect(api.listUsers).not.toHaveBeenCalled();
});

it("links a chosen identity explicitly and reloads the current account identities", async () => {
  const linked = {
    ...identity,
    provider: "github" as const,
    id: "github:account-123",
    email: "same@example.test",
    email_verified: true,
  };
  const api = fixture({
    listIdentities: vi
      .fn()
      .mockResolvedValueOnce([identity])
      .mockResolvedValue([identity, linked]),
  });
  const navigation = vi.fn().mockResolvedValue(fixtureSession);
  render(<Console api={api} onAuthorization={navigation} />);
  fireEvent.click(await screen.findByRole("button", { name: "My account" }));
  fireEvent.click(await screen.findByRole("button", { name: "Link GitHub" }));
  await screen.findByText("github:account-123");
  expect(api.linkIdentity).toHaveBeenCalledWith("github");
  expect(api.login).not.toHaveBeenCalled();
  expect(navigation).toHaveBeenCalledWith("https://oauth.example.test/link");
  expect(
    (screen.getByRole("button", { name: "GitHub linked" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});

it("shows linking errors and does not fabricate a successful binding", async () => {
  const api = fixture({
    linkIdentity: vi
      .fn()
      .mockRejectedValue(
        new APIError(
          409,
          "identity_conflict",
          "Identity belongs to another account.",
        ),
      ),
  });
  render(<Console api={api} onAuthorization={completeAuthorization} />);
  fireEvent.click(await screen.findByRole("button", { name: "My account" }));
  fireEvent.click(await screen.findByRole("button", { name: "Link Google" }));
  await screen.findByText("Identity belongs to another account.");
  expect(screen.queryByRole("button", { name: "Google linked" })).toBeNull();
  expect(api.listIdentities).toHaveBeenCalledTimes(1);
});

it("limits the administrator directory to summaries and loads opaque pages without duplicate concurrent requests", async () => {
  const more = Promise.withResolvers<UserPage>();
  const first = {
    id: "usr_other",
    name: "Other member",
    email: "other@example.test",
    created_at: fixtureUser.created_at,
    app_count: 3,
  };
  const last = {
    id: "usr_private",
    name: null,
    email: null,
    created_at: fixtureUser.created_at,
    app_count: 0,
  };
  const api = fixture({
    session: vi.fn().mockResolvedValue({
      ...fixtureSession,
      user: { ...fixtureUser, is_admin: true },
    }),
    listUsers: vi
      .fn()
      .mockResolvedValueOnce({ users: [first], next_cursor: "opaque/page+two" })
      .mockReturnValue(more.promise),
  });
  render(<Console api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: "Users" }));
  await screen.findByText(first.id);
  const table = screen.getByRole("table");
  expect(
    within(table)
      .getAllByRole("columnheader")
      .map((cell) => cell.textContent),
  ).toEqual(["User", "Created", "Applications"]);
  expect(within(table).queryByRole("button")).toBeNull();
  expect(within(table).queryByRole("link")).toBeNull();
  const button = screen.getByRole("button", { name: "Load more users" });
  act(() => {
    button.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    button.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  expect(api.listUsers).toHaveBeenCalledTimes(2);
  expect(api.listUsers).toHaveBeenLastCalledWith("opaque/page+two");
  await act(async () =>
    more.resolve({ users: [first, last], next_cursor: null }),
  );
  await screen.findByText(last.id);
  expect(screen.getAllByText(first.id)).toHaveLength(1);
  expect(screen.queryByRole("button", { name: "Load more users" })).toBeNull();
});

it("retries the failed next user page while retaining previously loaded summaries", async () => {
  const summary = {
    id: "usr_visible",
    name: "Visible",
    email: null,
    created_at: fixtureUser.created_at,
    app_count: 2,
  };
  const api = fixture({
    session: vi.fn().mockResolvedValue({
      ...fixtureSession,
      user: { ...fixtureUser, is_admin: true },
    }),
    listUsers: vi
      .fn()
      .mockResolvedValueOnce({ users: [summary], next_cursor: "next" })
      .mockRejectedValueOnce(new Error("Next page unavailable."))
      .mockResolvedValue({ users: [], next_cursor: null }),
  });
  render(<Console api={api} />);
  fireEvent.click(await screen.findByRole("button", { name: "Users" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Load more users" }),
  );
  await screen.findByText("Next page unavailable.");
  expect(screen.getByText(summary.id)).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "Retry users" }));
  await waitFor(() => expect(api.listUsers).toHaveBeenCalledTimes(3));
  expect(api.listUsers).toHaveBeenLastCalledWith("next");
  await waitFor(() =>
    expect(screen.queryByText("Next page unavailable.")).toBeNull(),
  );
});

it.each([
  [
    "invalid_callback",
    "This sign-in request is expired, already used, or no longer valid. Start a new sign-in.",
  ],
  [
    "login_unavailable",
    "This sign-in request is expired, already used, or no longer valid. Start a new sign-in.",
  ],
  ["__proto__", "Sign-in could not be completed. Please try again."],
  [
    "provider_failed",
    "The provider canceled or could not complete sign-in. Please try again.",
  ],
  [
    "identity_conflict",
    "This identity belongs to another account and cannot be linked.",
  ],
  [
    "unrecognized_external_message",
    "Sign-in could not be completed. Please try again.",
  ],
])(
  "renders a fixed callback error for %s and removes it from the browser URL",
  async (code, text) => {
    window.history.replaceState(
      null,
      "",
      `/console/?login_error=${code}&keep=1`,
    );
    const api = fixture({
      session: vi
        .fn()
        .mockRejectedValue(
          new APIError(401, "unauthorized", "Sign in required."),
        ),
    });
    render(<Console api={api} />);
    await screen.findByText(text);
    expect(window.location.search).toBe("?keep=1");
    expect(screen.queryByText("unrecognized_external_message")).toBeNull();
  },
);

it("holds navigation, sign-out and duplicate linking while the OAuth start response is pending", async () => {
  const pending = Promise.withResolvers<{ authorization_url: string }>();
  const api = fixture({
    linkIdentity: vi.fn().mockReturnValue(pending.promise),
    logout: vi.fn().mockResolvedValue(undefined),
  });
  const navigation = vi.fn().mockResolvedValue(fixtureSession);
  render(<Console api={api} onAuthorization={navigation} />);
  fireEvent.click(await screen.findByRole("button", { name: "My account" }));
  const link = await screen.findByRole("button", { name: "Link GitHub" });
  const applications = screen.getByRole("button", { name: "Applications0" });
  const logout = screen.getByRole("button", { name: "Sign out" });
  act(() => {
    link.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    applications.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    logout.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    link.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  expect(api.linkIdentity).toHaveBeenCalledTimes(1);
  expect(api.logout).not.toHaveBeenCalled();
  expect(screen.getByRole("heading", { name: "My account" })).toBeDefined();
  await act(async () =>
    pending.resolve({ authorization_url: "https://oauth.example.test/link" }),
  );
  await waitFor(() => expect(navigation).toHaveBeenCalledTimes(1));
});

it("discovers public providers and completes OAuth from the real signed-out interactive preview", async () => {
  const api = createMockAPI("developer", true);
  await expect(api.listApps()).rejects.toMatchObject({ status: 401 });
  render(
    <Console api={api} preview onAuthorization={api.completeAuthorization} />,
  );
  const github = await screen.findByRole("button", {
    name: "Continue with GitHub",
  });
  expect(
    screen.getByRole("button", { name: "Continue with Microsoft" }),
  ).toBeDefined();
  expect(
    screen.getByRole("button", { name: "Continue with Google" }),
  ).toBeDefined();
  expect(
    screen.queryByRole("button", { name: "Retry sign-in methods" }),
  ).toBeNull();
  fireEvent.click(github);
  await screen.findByRole("button", { name: "Manage Syntrix" });
  expect(screen.getByText("Alex Morgan")).toBeDefined();
  expect(screen.queryByRole("button", { name: "Users" })).toBeNull();
});

it("preserves an identity-conflict callback notice through authenticated session restoration and successful workspace loading", async () => {
  window.history.replaceState(
    null,
    "",
    "/console/?login_error=identity_conflict",
  );
  const session = Promise.withResolvers<typeof fixtureSession>();
  const applications = Promise.withResolvers<never[]>();
  const api = fixture({
    session: vi.fn().mockReturnValue(session.promise),
    listApps: vi.fn().mockReturnValue(applications.promise),
  });
  render(<Console api={api} />);
  await act(async () => session.resolve(fixtureSession));
  const notice = await screen.findByText(
    "This identity belongs to another account and cannot be linked.",
  );
  expect(window.location.search).toBe("");
  await act(async () => applications.resolve([]));
  await screen.findByRole("button", { name: "New application" });
  expect(screen.getByText(notice.textContent as string)).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "My account" }));
  await screen.findByText(identity.id);
  expect(
    screen.getByText(
      "This identity belongs to another account and cannot be linked.",
    ),
  ).toBeDefined();
  fireEvent.click(
    screen.getByRole("button", { name: "Dismiss sign-in notice" }),
  );
  expect(
    screen.queryByText(
      "This identity belongs to another account and cannot be linked.",
    ),
  ).toBeNull();
});
