import { afterEach, beforeAll, expect, it, vi } from "vitest";
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
import { createAPI } from "./api";
import type { Application, AppInput } from "./api";

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute("open");
  };
});
afterEach(cleanup);
const initial: Application = {
  id: "app_example",
  name: "Example",
  enabled: true,
  redirect_uris: ["https://example.test/callback"],
  providers: ["github"],
  created_at: "2026-10-12T09:00:00Z",
  updated_at: "2026-10-12T09:00:00Z",
};
const json = (value: unknown) =>
  new Response(JSON.stringify(value), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (cause: Error) => void;
  const promise = new Promise<T>((accept, fail) => {
    resolve = accept;
    reject = fail;
  });
  return { promise, resolve, reject };
};
const transport = () => {
  let persisted = structuredClone(initial);
  const updates: AppInput[] = [];
  const firstUpdate = deferred<Response>();
  const deletion = deferred<Response>();
  let deleted = false;
  let deleteRequests = 0;
  let listFailure = false;
  const fetcher = vi
    .fn<typeof fetch>()
    .mockImplementation(async (url, options) => {
      if (url === "/console/api/session") return json({ csrf_token: "token" });
      if (url === "/console/api/apps") {
        if (listFailure) throw new Error("Registry unavailable.");
        return json({ apps: deleted ? [] : [persisted] });
      }
      if (url === "/console/api/providers")
        return json({ providers: [{ id: "github", available: true }] });
      if (url === "/console/api/apps/app_example/secrets")
        return json({ secrets: [] });
      if (
        url === "/console/api/apps/app_example" &&
        options?.method === "PUT"
      ) {
        const input = JSON.parse(options.body as string) as AppInput;
        updates.push(input);
        persisted = { ...persisted, ...input };
        return updates.length === 1 ? firstUpdate.promise : json(persisted);
      }
      if (
        url === "/console/api/apps/app_example" &&
        options?.method === "DELETE"
      ) {
        deleteRequests += 1;
        deleted = true;
        return deletion.promise;
      }
      throw new Error(`Unexpected request ${options?.method} ${String(url)}`);
    });
  return {
    api: createAPI(fetcher),
    firstUpdate,
    updates,
    deletion,
    deleteRequests: () => deleteRequests,
    failList: (value: boolean) => {
      listFailure = value;
    },
    persisted: () => persisted,
  };
};
const openApp = async () => {
  fireEvent.click(
    await screen.findByRole("button", { name: "Manage Example" }),
  );
};
const editName = (name: string) => {
  const editor = screen.getByRole("dialog", { name: "Edit application" });
  fireEvent.change(within(editor).getByLabelText("Application name"), {
    target: { value: name },
  });
  return editor;
};
it("serializes editing after a committed disable response and preserves the actual disabled state", async () => {
  const server = transport();
  render(<Console api={server.api} />);
  await openApp();
  fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
  await waitFor(() => expect(server.persisted().enabled).toBe(false));
  expect(screen.getByText("Updating…")).toBeDefined();
  const edit = screen.getByRole("button", {
    name: "Edit application",
  }) as HTMLButtonElement;
  const remove = screen.getByRole("button", {
    name: "Delete application",
  }) as HTMLButtonElement;
  expect(edit.disabled).toBe(true);
  expect(remove.disabled).toBe(true);
  fireEvent.click(edit);
  fireEvent.click(remove);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(server.updates).toHaveLength(1);
  await act(async () =>
    server.firstUpdate.resolve(json({ ...initial, enabled: false })),
  );
  fireEvent.click(screen.getByRole("button", { name: "Edit application" }));
  const editor = editName("Renamed");
  expect(
    (
      within(editor).getByRole("checkbox", {
        name: /Enable application/,
      }) as HTMLInputElement
    ).checked,
  ).toBe(false);
  fireEvent.click(within(editor).getByRole("button", { name: "Save changes" }));
  await screen.findByRole("heading", { name: "Renamed" });
  expect(server.persisted().enabled).toBe(false);
  expect(server.updates).toHaveLength(2);
  expect(screen.getByRole("button", { name: "Enable app" })).toBeDefined();
});

it("guards a pending editor against status changes, deletion, duplicate submits, and navigation", async () => {
  const server = transport();
  render(<Console api={server.api} />);
  await openApp();
  fireEvent.click(screen.getByRole("button", { name: "Edit application" }));
  const editor = editName("Renamed");
  fireEvent.click(
    within(editor).getByRole("checkbox", { name: /Enable application/ }),
  );
  fireEvent.change(within(editor).getByLabelText("Callback URLs"), {
    target: { value: "https://renamed.test/callback" },
  });
  fireEvent.submit(editor.querySelector("form")!);
  fireEvent.submit(editor.querySelector("form")!);
  await waitFor(() => expect(server.updates).toHaveLength(1));
  fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
  fireEvent.click(screen.getByRole("button", { name: "Delete application" }));
  fireEvent.click(screen.getByRole("button", { name: "← All applications" }));
  fireEvent.click(screen.getByRole("button", { name: "Identity providers" }));
  expect(
    screen.getByRole("dialog", { name: "Edit application" }),
  ).toBeDefined();
  expect(
    screen.getByRole("button", { name: "← All applications" }),
  ).toBeDefined();
  expect(
    screen.queryByRole("dialog", { name: "Delete application?" }),
  ).toBeNull();
  expect(server.updates).toHaveLength(1);
  await act(async () => server.firstUpdate.resolve(json(server.persisted())));
  await screen.findByRole("heading", { name: "Renamed" });
  expect(screen.getByRole("button", { name: "Enable app" })).toBeDefined();
  fireEvent.click(screen.getByRole("button", { name: "Edit application" }));
  const reopened = screen.getByRole("dialog", { name: "Edit application" });
  expect(
    (within(reopened).getByLabelText("Application name") as HTMLInputElement)
      .value,
  ).toBe("Renamed");
  expect(
    (within(reopened).getByLabelText("Callback URLs") as HTMLTextAreaElement)
      .value,
  ).toBe("https://renamed.test/callback");
  expect(
    (
      within(reopened).getByRole("checkbox", {
        name: /Enable application/,
      }) as HTMLInputElement
    ).checked,
  ).toBe(false);
});

it("unlocks application actions after a status request fails without fabricating a saved state", async () => {
  const server = transport();
  render(<Console api={server.api} />);
  await openApp();
  fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
  await waitFor(() => expect(server.updates).toHaveLength(1));
  await act(async () =>
    server.firstUpdate.reject(new Error("Connection interrupted.")),
  );
  await screen.findByText("Connection interrupted.");
  expect(screen.queryByText("Updating…")).toBeNull();
  expect(
    (
      screen.getByRole("button", {
        name: "Edit application",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Edit application" }));
  const editor = screen.getByRole("dialog", { name: "Edit application" });
  expect(
    (within(editor).getByLabelText("Application name") as HTMLInputElement)
      .value,
  ).toBe("Example");
  fireEvent.click(within(editor).getByRole("button", { name: "Cancel" }));
  fireEvent.click(screen.getByRole("button", { name: "Delete application" }));
  expect(
    screen.getByRole("dialog", { name: "Delete application?" }),
  ).toBeDefined();
});

it("retains a failed edit draft for retry and releases its reservation on explicit cancellation", async () => {
  const server = transport();
  render(<Console api={server.api} />);
  await openApp();
  fireEvent.click(screen.getByRole("button", { name: "Edit application" }));
  const editor = editName("Draft name");
  fireEvent.click(within(editor).getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(server.updates).toHaveLength(1));
  await act(async () => server.firstUpdate.reject(new Error("Save failed.")));
  await screen.findByText("Save failed.");
  expect(
    (within(editor).getByLabelText("Application name") as HTMLInputElement)
      .value,
  ).toBe("Draft name");
  expect(
    (
      within(editor).getByRole("button", {
        name: "Save changes",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
  expect(
    (screen.getByRole("button", { name: "Disable app" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  fireEvent.click(within(editor).getByRole("button", { name: "Cancel" }));
  expect(
    (screen.getByRole("button", { name: "Disable app" }) as HTMLButtonElement)
      .disabled,
  ).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
  await screen.findByRole("button", { name: "Enable app" });
  expect(server.updates).toHaveLength(2);
});

it("requires a verified refresh before editing after both a lost write response and reconciliation failure", async () => {
  const server = transport();
  render(<Console api={server.api} />);
  await openApp();
  fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
  await waitFor(() => expect(server.persisted().enabled).toBe(false));
  server.failList(true);
  await act(async () => server.firstUpdate.reject(new Error("Response lost.")));
  await screen.findByText("State unavailable");
  expect(
    (
      screen.getByRole("button", {
        name: "Edit application",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    (
      screen.getByRole("button", {
        name: "Delete application",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(screen.getByRole("alert").textContent).toContain(
    "Registry unavailable.",
  );
  server.failList(false);
  fireEvent.click(screen.getByRole("button", { name: "Refresh application" }));
  await waitFor(() =>
    expect(screen.queryByText("State unavailable")).toBeNull(),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Edit application",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Edit application" }));
  const editor = editName("Safe rename");
  expect(
    (
      within(editor).getByRole("checkbox", {
        name: /Enable application/,
      }) as HTMLInputElement
    ).checked,
  ).toBe(false);
  fireEvent.click(within(editor).getByRole("button", { name: "Save changes" }));
  await screen.findByRole("heading", { name: "Safe rename" });
  expect(server.persisted().enabled).toBe(false);
});

it("reserves deletion through the confirmed request and prevents duplicate deletes or competing configuration", async () => {
  const server = transport();
  render(<Console api={server.api} />);
  await openApp();
  fireEvent.click(screen.getByRole("button", { name: "Delete application" }));
  const confirmation = screen.getByRole("dialog", {
    name: "Delete application?",
  });
  const deleteButton = within(confirmation).getByRole("button", {
    name: "Delete application",
  });
  fireEvent.click(deleteButton);
  fireEvent.click(deleteButton);
  await screen.findByText("Deleting…");
  fireEvent.click(screen.getByRole("button", { name: "Edit application" }));
  fireEvent.click(screen.getByRole("button", { name: "Disable app" }));
  fireEvent.click(screen.getByRole("button", { name: "← All applications" }));
  expect(screen.queryByRole("dialog", { name: "Edit application" })).toBeNull();
  expect(server.updates).toHaveLength(0);
  expect(server.deleteRequests()).toBe(1);
  await act(async () =>
    server.deletion.resolve(new Response(null, { status: 204 })),
  );
  await screen.findByRole("heading", { name: "Your products belong here" });
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("rejects competing events before pending controls have rendered", async () => {
  const server = transport();
  render(<Console api={server.api} />);
  await openApp();
  const disable = screen.getByRole("button", { name: "Disable app" });
  const edit = screen.getByRole("button", { name: "Edit application" });
  const remove = screen.getByRole("button", { name: "Delete application" });
  act(() => {
    disable.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    edit.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    remove.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  });
  expect(server.updates).toHaveLength(1);
  expect(screen.queryByRole("dialog")).toBeNull();
  await act(async () => server.firstUpdate.resolve(json(server.persisted())));
  expect(screen.getByRole("button", { name: "Enable app" })).toBeDefined();
});
