import { expect, test } from "@playwright/test";
import type { Locator, Page } from "@playwright/test";

const value = (dialog: Locator, label: string) =>
  dialog.locator(".copy-field").filter({ hasText: label }).locator("code");

const saveCredential = async (page: Page) => {
  const dialog = page.getByRole("dialog", { name: "Save your app secret" });
  await expect(dialog).toBeVisible();
  const secret = await value(dialog, "App secret").innerText();
  const id = await value(dialog, "Secret ID").innerText();
  const appID = await value(dialog, "App ID").innerText();
  expect(secret.length).toBeGreaterThanOrEqual(43);
  await expect(dialog.getByRole("button", { name: "Done" })).toBeDisabled();
  await dialog.getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Done" }).click();
  await expect(dialog).not.toBeVisible();
  await expect(page.getByText(secret, { exact: true })).toHaveCount(0);
  return { secret, id, appID };
};

test("administrator manages applications and one-time credentials against the service", async ({
  page,
  browser,
  context,
}) => {
  const failures: string[] = [];
  let secretCreations = 0;
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().endsWith("/secrets"))
      secretCreations += 1;
  });
  page.on("pageerror", (error) => failures.push(error.message));
  await page.goto("/console/");
  await expect(
    page.getByRole("heading", { name: "Welcome to your Console" }),
  ).toBeVisible();
  await page.getByLabel("Administrator password").fill("incorrect");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("password is incorrect");
  await page
    .getByLabel("Administrator password")
    .fill("browser-test-administrator-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Your products belong here" }),
  ).toBeVisible();

  const cookies = await context.cookies();
  expect(cookies).toHaveLength(1);
  expect(cookies[0]).toMatchObject({
    name: "uniauth_admin",
    httpOnly: true,
    sameSite: "Strict",
    secure: false,
    path: "/",
  });
  expect(await page.evaluate(() => document.cookie)).not.toContain(
    "uniauth_admin",
  );
  const anonymous = await browser.newContext();
  try {
    const anonymousPage = await anonymous.newPage();
    await anonymousPage.goto("http://127.0.0.1:18080/console/");
    await expect(
      anonymousPage.getByLabel("Administrator password"),
    ).toBeVisible();
    expect(
      (
        await anonymous.request.get("http://127.0.0.1:18080/console/api/apps")
      ).status(),
    ).toBe(401);
  } finally {
    await anonymous.close();
  }

  await page
    .getByRole("button", { name: "Identity providers", exact: true })
    .click();
  await expect(page.locator(".provider-card")).toHaveCount(3);
  await expect(
    page.locator(".provider-card").getByText("Not configured", { exact: true }),
  ).toHaveCount(3);
  await expect(
    page.getByText("Personal Microsoft accounts", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: /^Applications/ }).click();
  await page.getByRole("button", { name: "New application" }).click();
  const editor = page.getByRole("dialog", {
    name: "Create application",
    exact: true,
  });
  await editor.getByLabel("Application name").fill("Browser product");
  await editor
    .getByLabel("Secret name", { exact: true })
    .fill("Production backend");
  await editor
    .getByLabel("Description", { exact: true })
    .fill("  Deployment owner\n生产环境 🔑  ");
  await editor
    .getByLabel("Callback URLs", { exact: true })
    .fill("https://product.example/auth/callback?code=reserved");
  await editor.getByRole("checkbox", { name: /Google/ }).check();
  const invalid = page.waitForResponse(
    (response) =>
      response.url().endsWith("/console/api/apps") &&
      response.request().method() === "POST",
  );
  await editor
    .getByRole("button", { name: "Create application", exact: true })
    .click();
  expect((await invalid).status()).toBe(400);
  await expect(editor.getByRole("alert")).toContainText(
    "The application configuration or credential metadata is invalid.",
  );
  await expect(editor.getByLabel("Application name")).toHaveValue(
    "Browser product",
  );
  await editor
    .getByLabel("Callback URLs", { exact: true })
    .fill(
      "https://product.example/auth/callback\nhttps://staging.product.example/auth/callback",
    );
  await editor.getByRole("checkbox", { name: /Microsoft/ }).check();
  await editor.getByRole("checkbox", { name: /GitHub/ }).check();
  await editor
    .getByRole("button", { name: "Create application", exact: true })
    .click();
  const firstDialog = page.getByRole("dialog", {
    name: "Save your app secret",
  });
  await expect(
    firstDialog.getByText("Production backend", { exact: true }),
  ).toBeVisible();
  await expect(firstDialog.locator(".secret-description")).toHaveText(
    "  Deployment owner\n生产环境 🔑  ",
  );
  const first = await saveCredential(page);
  await expect(
    page.getByRole("heading", { name: "Browser product", exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(".configuration").getByText("Not configured", { exact: true }),
  ).toHaveCount(3);
  await expect(
    page.getByRole("row").filter({ hasText: first.id }),
  ).toContainText("Active");

  await page
    .getByRole("button", { name: "Edit application", exact: true })
    .click();
  const edit = page.getByRole("dialog", {
    name: "Edit application",
    exact: true,
  });
  await edit.getByLabel("Application name").fill("Updated browser product");
  await edit
    .getByLabel("Callback URLs", { exact: true })
    .fill("https://product.example/auth/updated");
  await edit.getByRole("checkbox", { name: /Microsoft/ }).uncheck();
  await edit.getByRole("button", { name: "Save changes" }).click();
  await expect(edit).not.toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Updated browser product", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".url-list")).toHaveText(
    "https://product.example/auth/updated",
  );
  await expect(
    page.locator(".configuration").getByText("Microsoft", { exact: true }),
  ).toHaveCount(0);
  const appURL = `http://127.0.0.1:18080/console/api/apps/${first.appID}`;
  const committed = Promise.withResolvers<void>();
  const releaseResponse = Promise.withResolvers<void>();
  let updates = 0;
  await page.route(appURL, async (route) => {
    if (route.request().method() !== "PUT") {
      await route.continue();
      return;
    }
    updates += 1;
    try {
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      committed.resolve();
      await releaseResponse.promise;
      await route.fulfill({ response });
    } catch (error) {
      committed.reject(error);
      throw error;
    }
  });
  const disabledResponse = page.waitForResponse(
    (response) =>
      response.url() === appURL && response.request().method() === "PUT",
  );
  try {
    await page
      .getByRole("button", { name: "Disable app", exact: true })
      .click();
    await committed.promise;
    const stored = await context.request.get(appURL);
    expect(stored.ok()).toBe(true);
    expect((await stored.json()).enabled).toBe(false);
    await expect(
      page.locator(".configuration").getByText("Updating…", { exact: true }),
    ).toBeVisible();
    const editButton = page.getByRole("button", {
      name: "Edit application",
      exact: true,
    });
    await expect(editButton).toBeDisabled();
    await editButton.evaluate((button) =>
      (button as HTMLButtonElement).click(),
    );
    await expect(edit).not.toBeVisible();
    expect(updates).toBe(1);
  } finally {
    releaseResponse.resolve();
  }
  expect((await disabledResponse).ok()).toBe(true);
  await page.unroute(appURL);
  await expect(
    page.locator(".configuration").getByText("Disabled", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Edit application", exact: true })
    .click();
  await expect(
    edit.getByRole("checkbox", { name: /Enable application/ }),
  ).not.toBeChecked();
  await edit.getByLabel("Application name").fill("Serialized browser product");
  await edit.getByRole("button", { name: "Save changes" }).click();
  await expect(edit).not.toBeVisible();
  await expect(
    page.getByRole("heading", {
      name: "Serialized browser product",
      exact: true,
    }),
  ).toBeVisible();
  const edited = await context.request.get(appURL);
  expect(edited.ok()).toBe(true);
  expect(await edited.json()).toMatchObject({
    name: "Serialized browser product",
    enabled: false,
  });
  await page.getByRole("button", { name: "Enable app", exact: true }).click();
  await expect(
    page.locator(".configuration").getByText("Enabled", { exact: true }),
  ).toBeVisible();

  await page
    .getByRole("button", { name: "Create secret", exact: true })
    .click();
  const secretEditor = page.getByRole("dialog", {
    name: "Create secret",
    exact: true,
  });
  await secretEditor
    .getByLabel("Secret name", { exact: true })
    .fill("Staging backend");
  await secretEditor
    .getByLabel("Description", { exact: true })
    .fill("  Staging\nOwner: Integration team  ");
  const secretURL = `${appURL}/secrets`;
  const issuedCommitted = Promise.withResolvers<void>();
  const releaseIssuance = Promise.withResolvers<void>();
  let issuanceRequests = 0;
  await page.route(secretURL, async (route) => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    issuanceRequests += 1;
    try {
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      issuedCommitted.resolve();
      await releaseIssuance.promise;
      await route.fulfill({ response });
    } catch (error) {
      issuedCommitted.reject(error);
      throw error;
    }
  });
  try {
    await secretEditor
      .getByRole("button", { name: "Create secret", exact: true })
      .click();
    await issuedCommitted.promise;
    await expect(
      secretEditor.getByRole("button", { name: "Saving…" }),
    ).toBeDisabled();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeDisabled();
    await page
      .getByRole("button", { name: "Delete application", exact: true })
      .evaluate((button) => (button as HTMLButtonElement).click());
    await page
      .getByRole("button", { name: "← All applications" })
      .evaluate((button) => (button as HTMLButtonElement).click());
    await expect(
      page.getByRole("dialog", { name: "Delete application?" }),
    ).not.toBeVisible();
    expect(issuanceRequests).toBe(1);
  } finally {
    releaseIssuance.resolve();
  }
  const replacement = await saveCredential(page);
  await page.unroute(secretURL);
  await page
    .getByRole("button", {
      name: `Edit details for secret ${replacement.id}`,
      exact: true,
    })
    .click();
  const details = page.getByRole("dialog", { name: "Edit secret details" });
  await expect(details.getByLabel("Description", { exact: true })).toHaveValue(
    "  Staging\nOwner: Integration team  ",
  );
  await details
    .getByLabel("Secret name", { exact: true })
    .fill("Production replacement");
  await details.getByLabel("Description", { exact: true }).fill("");
  const metadataResponse = page.waitForResponse(
    (response) =>
      response.url() === `${secretURL}/${replacement.id}` &&
      response.request().method() === "PUT",
  );
  await details.getByRole("button", { name: "Save details" }).click();
  const metadata = await (await metadataResponse).json();
  expect(metadata).toMatchObject({
    id: replacement.id,
    name: "Production replacement",
    description: "",
  });
  expect(metadata).not.toHaveProperty("secret");
  await expect(details).not.toBeVisible();
  await expect(
    page.getByRole("row").filter({ hasText: replacement.id }),
  ).toContainText("No description");
  expect(replacement.secret).not.toBe(first.secret);
  expect(replacement.id).not.toBe(first.id);
  await page
    .getByRole("button", {
      name: `Reset secret ${replacement.id}`,
      exact: true,
    })
    .click();
  const reset = page.getByRole("dialog", { name: "Reset this secret?" });
  await expect(reset).toContainText(replacement.id);
  await expect(reset).toContainText("Production replacement");
  await expect(reset).toContainText("stop working immediately");
  await expect(reset).toContainText("shown once");
  await reset.getByRole("button", { name: "Cancel" }).click();
  await expect(reset).not.toBeVisible();
  await expect(
    page.getByRole("row").filter({ hasText: replacement.id }),
  ).toContainText("Active");
  const afterCancel = await context.request.get(
    `/console/api/apps/${first.appID}/secrets`,
  );
  expect(afterCancel.ok()).toBe(true);
  expect(
    (await afterCancel.json()).secrets.find(
      (secret: { id: string }) => secret.id === replacement.id,
    ).revoked_at,
  ).toBeUndefined();
  await page
    .getByRole("button", {
      name: `Reset secret ${replacement.id}`,
      exact: true,
    })
    .click();
  await reset.getByRole("button", { name: "Reset secret" }).click();
  const resetDialog = page.getByRole("dialog", {
    name: "Save your app secret",
  });
  await expect(resetDialog).toContainText("Production replacement");
  await expect(resetDialog).toContainText("No description");
  const resetCredential = await saveCredential(page);
  expect(resetCredential.secret).not.toBe(replacement.secret);
  expect(resetCredential.id).not.toBe(replacement.id);
  await expect(
    page.getByRole("row").filter({ hasText: replacement.id }),
  ).toContainText("Revoked");
  await expect(
    page.getByRole("row").filter({ hasText: resetCredential.id }),
  ).toContainText("Active");
  const retrieveWith = async (secret: string) => {
    const response = await context.request.post("/auth/result", {
      headers: {
        Authorization: `Basic ${Buffer.from(`${first.appID}:${secret}`).toString("base64")}`,
        "Content-Type": "application/json",
      },
      data: { code: "unknown-result-code" },
    });
    return { status: response.status(), body: await response.json() };
  };
  expect(await retrieveWith(replacement.secret)).toMatchObject({
    status: 401,
    body: { error: { code: "invalid_app" } },
  });
  expect(await retrieveWith(resetCredential.secret)).toMatchObject({
    status: 410,
    body: { error: { code: "result_unavailable" } },
  });
  await page
    .getByRole("button", { name: `Revoke secret ${first.id}`, exact: true })
    .click();
  const revoke = page.getByRole("dialog", { name: "Revoke this secret?" });
  await expect(revoke).toContainText("Production backend");
  await revoke
    .getByRole("button", { name: "Revoke secret", exact: true })
    .click();
  await expect(revoke).not.toBeVisible();
  await expect(
    page.getByRole("row").filter({ hasText: first.id }),
  ).toContainText("Revoked");
  await expect(
    page.getByRole("row").filter({ hasText: replacement.id }),
  ).toContainText("Revoked");
  await expect(
    page.getByRole("row").filter({ hasText: resetCredential.id }),
  ).toContainText("Active");
  await page
    .getByRole("button", {
      name: `Edit details for secret ${first.id}`,
      exact: true,
    })
    .click();
  await details
    .getByLabel("Secret name", { exact: true })
    .fill("Retired production");
  await details
    .getByLabel("Description", { exact: true })
    .fill("  Retained for audit\n替换记录  ");
  await details.getByRole("button", { name: "Save details" }).click();
  await expect(details).not.toBeVisible();
  await expect(
    page.getByRole("row").filter({ hasText: first.id }),
  ).toContainText("Retired production");
  expect(secretCreations).toBe(1);
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Applications", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", {
      name: "Manage Serialized browser product",
      exact: true,
    })
    .click();
  await expect(page.getByText(first.secret, { exact: true })).toHaveCount(0);
  await expect(page.getByText(replacement.secret, { exact: true })).toHaveCount(
    0,
  );
  await expect(
    page.getByText(resetCredential.secret, { exact: true }),
  ).toHaveCount(0);
  const listed = await context.request.get(
    `/console/api/apps/${first.appID}/secrets`,
  );
  expect(listed.ok()).toBe(true);
  const listedBody = await listed.json();
  expect(listedBody.secrets).toHaveLength(3);
  expect(listedBody.secrets).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        id: first.id,
        name: "Retired production",
        description: "  Retained for audit\n替换记录  ",
        revoked_at: expect.any(String),
      }),
      expect.objectContaining({
        id: replacement.id,
        name: "Production replacement",
        description: "",
        revoked_at: expect.any(String),
      }),
      expect.objectContaining({
        id: resetCredential.id,
        name: "Production replacement",
        description: "",
      }),
    ]),
  );
  await expect(
    page.getByRole("row").filter({ hasText: first.id }),
  ).toContainText("Retired production");
  await expect(
    page.getByRole("row").filter({ hasText: replacement.id }),
  ).toContainText("No description");
  const listing = JSON.stringify(listedBody);
  expect(listing).not.toContain(first.secret);
  expect(listing).not.toContain(replacement.secret);
  expect(listing).not.toContain(resetCredential.secret);

  await page
    .getByRole("button", { name: "Delete application", exact: true })
    .click();
  const remove = page.getByRole("dialog", { name: "Delete application?" });
  await remove
    .getByRole("button", { name: "Delete application", exact: true })
    .click();
  await expect(remove).not.toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Your products belong here" }),
  ).toBeVisible();
  expect(
    (await context.request.get(`/console/api/apps/${first.appID}`)).status(),
  ).toBe(404);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByLabel("Administrator password")).toBeVisible();
  expect((await context.request.get("/console/api/session")).status()).toBe(
    401,
  );
  expect(await context.cookies()).toHaveLength(0);
  await page.reload();
  await expect(page.getByLabel("Administrator password")).toBeVisible();
  expect(failures).toEqual([]);
});
