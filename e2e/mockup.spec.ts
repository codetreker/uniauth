import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";

const secretRow = (page: Page, id: string) =>
  page.getByRole("row").filter({
    has: page.getByRole("button", {
      name: `Edit details for secret ${id}`,
      exact: true,
    }),
  });

test("final Console components provide an interactive desktop preview", async ({
  page,
}) => {
  const failures: string[] = [];
  page.on("pageerror", (error) => failures.push(error.message));
  await page.goto("/console/mockup.html");
  await expect(
    page.getByRole("heading", { name: "Applications", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Manage Borgee" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Manage Biz Table" }),
  ).toBeVisible();
  await page.screenshot({ path: ".tmp/mockup-desktop.png", fullPage: true });
  await page.getByRole("button", { name: "Manage Borgee" }).click();
  await expect(
    page.getByRole("heading", { name: "Borgee", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("tab", { name: "Active 3", exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await expect(
    page.getByRole("tab", { name: "Disabled 1", exact: true }),
  ).toBeVisible();
  await expect(secretRow(page, "sec_0_previous")).toHaveCount(0);
  await expect(
    secretRow(page, "sec_0_current").getByRole("cell").first(),
  ).toHaveText("Production backend");
  await expect(page.locator(".secrets-panel")).not.toContainText("sec_0_");
  await expect(page.locator(".secrets-panel")).not.toContainText(
    "Owner: Platform team",
  );
  await expect(page.locator(".secrets-panel")).not.toContainText(
    "No description",
  );
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: ".tmp/mockup-detail.png", fullPage: true });
  await page.screenshot({
    path: ".tmp/mockup-secrets-active.png",
    fullPage: true,
  });
  await page.getByRole("tab", { name: /^Active/ }).focus();
  await page.keyboard.press("End");
  await expect(page.getByRole("tab", { name: /^Disabled/ })).toBeFocused();
  await expect(secretRow(page, "sec_0_previous")).toContainText("Revoked");
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: ".tmp/mockup-secrets-disabled.png",
    fullPage: true,
  });
  await page.keyboard.press("Home");
  await expect(page.getByRole("tab", { name: /^Active/ })).toBeFocused();
  await page
    .getByRole("button", { name: "Reset secret sec_0_current" })
    .click();
  const reset = page.getByRole("dialog", { name: "Reset this secret?" });
  await expect(reset).toContainText("sec_0_current");
  await expect(reset).toContainText("Production backend");
  await expect(reset).toContainText("stop working immediately");
  await page.screenshot({
    path: ".tmp/mockup-reset-confirm.png",
  });
  await reset.getByRole("button", { name: "Reset secret" }).click();
  const resetCredential = page.getByRole("dialog", {
    name: "Save your app secret",
  });
  await expect(resetCredential).toContainText("sec_0_current has been revoked");
  await expect(resetCredential).toContainText("Production backend");
  await expect(resetCredential).toContainText("Owner: Platform team");
  await page.screenshot({
    path: ".tmp/mockup-reset-credential.png",
  });
  const replacementID = await resetCredential
    .locator(".copy-field")
    .filter({ hasText: "Secret ID" })
    .locator("code")
    .innerText();
  await resetCredential.getByRole("checkbox").check();
  await resetCredential.getByRole("button", { name: "Done" }).click();
  await expect(secretRow(page, "sec_0_current")).toHaveCount(0);
  await expect(secretRow(page, replacementID)).toContainText("Active");
  await page.getByRole("tab", { name: /^Disabled/ }).click();
  await expect(secretRow(page, "sec_0_current")).toContainText("Revoked");
  await page
    .getByRole("button", {
      name: "Edit details for secret sec_0_previous",
      exact: true,
    })
    .click();
  const details = page.getByRole("dialog", { name: "Edit secret details" });
  await details
    .getByLabel("Description", { exact: true })
    .fill("Retained deployment history\nOwner: Platform team");
  await page.screenshot({
    path: ".tmp/mockup-secret-edit.png",
    fullPage: true,
  });
  await details.getByRole("button", { name: "Save details" }).click();
  await expect(details).not.toBeVisible();
  await expect(secretRow(page, "sec_0_previous")).not.toContainText(
    "Retained deployment history",
  );
  await page
    .getByRole("button", {
      name: "Edit details for secret sec_0_previous",
      exact: true,
    })
    .click();
  await expect(details.getByLabel("Description", { exact: true })).toHaveValue(
    "Retained deployment history\nOwner: Platform team",
  );
  await details.getByRole("button", { name: "Cancel" }).click();

  await page.getByRole("button", { name: "← All applications" }).click();
  await page.getByRole("button", { name: "New application" }).click();
  const editor = page.getByRole("dialog", {
    name: "Create application",
    exact: true,
  });
  await editor.getByLabel("Application name").fill("New workspace");
  await editor
    .getByRole("checkbox", { name: /Create an initial secret/ })
    .check();
  await editor
    .getByLabel("Secret name", { exact: true })
    .fill("Production service");
  await editor
    .getByLabel("Description", { exact: true })
    .fill("Deployment credential\nOwner: Workspace team");
  await editor
    .getByLabel("Callback URLs", { exact: true })
    .fill("https://workspace.example/auth/callback");
  await page.screenshot({ path: ".tmp/mockup-create.png", fullPage: true });
  await editor
    .getByRole("button", { name: "Create application", exact: true })
    .click();
  const credential = page.getByRole("dialog", { name: "Save your app secret" });
  await expect(credential).toBeVisible();
  await expect(credential.getByRole("button", { name: "Done" })).toBeDisabled();
  await page.screenshot({ path: ".tmp/mockup-credential.png", fullPage: true });
  await credential.getByRole("checkbox").check();
  await credential.getByRole("button", { name: "Done" }).click();
  await expect(
    page.getByRole("heading", { name: "New workspace", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "← All applications" }).click();
  await page.getByRole("button", { name: "New application" }).click();
  const noSecretEditor = page.getByRole("dialog", {
    name: "Create application",
    exact: true,
  });
  await noSecretEditor.getByLabel("Application name").fill("HTTP preview");
  await noSecretEditor
    .getByLabel("Callback URLs", { exact: true })
    .fill("http://preview-product.example/callback");
  await noSecretEditor
    .getByRole("button", { name: "Create application", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "HTTP preview", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("dialog", { name: "Save your app secret" }),
  ).toHaveCount(0);
  await expect(
    page.getByText(
      "No active secrets. Create a secret to connect this application.",
    ),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Continue with Microsoft", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Continue with Microsoft", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Manage New workspace" }),
  ).toBeVisible();
  expect(failures).toEqual([]);
});

test("preview remains usable on a mobile viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/console/mockup.html");
  await expect(
    page.getByRole("heading", { name: "Applications", exact: true }),
  ).toBeVisible();
  await page.screenshot({ path: ".tmp/mockup-mobile.png", fullPage: true });
  const dimensions = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth,
  }));
  expect(dimensions.document).toBeLessThanOrEqual(dimensions.viewport);
  await page.getByRole("button", { name: "Manage Biz Table" }).click();
  await expect(secretRow(page, "sec_2_unicode")).toContainText("开发环境");
  await page.screenshot({
    path: ".tmp/mockup-secret-mobile.png",
    fullPage: true,
  });
  const detailDimensions = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth,
  }));
  expect(detailDimensions.document).toBeLessThanOrEqual(
    detailDimensions.viewport,
  );
  await page.getByRole("tab", { name: "Disabled 1", exact: true }).click();
  await expect(secretRow(page, "sec_2_previous")).toContainText("Revoked");
  await expect(page.getByRole("button", { name: /^Reset secret/ })).toHaveCount(
    0,
  );
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({
    path: ".tmp/mockup-secrets-disabled-mobile.png",
    fullPage: true,
  });
  await page.getByRole("tab", { name: "Active 3", exact: true }).click();
  await page
    .getByRole("button", {
      name: "Edit details for secret sec_2_unicode",
      exact: true,
    })
    .click();
  const details = page.getByRole("dialog", { name: "Edit secret details" });
  await expect(details.getByLabel("Description", { exact: true })).toHaveValue(
    /Preserve these spaces/,
  );
  await page.screenshot({
    path: ".tmp/mockup-secret-edit-mobile.png",
    fullPage: true,
  });
  await details.getByRole("button", { name: "Cancel" }).click();
  await expect(
    page.getByRole("button", { name: "Enable app", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Enable app", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Disable app", exact: true }),
  ).toBeVisible();
});

test("platform preview exposes final OAuth, account and limited administrator screens", async ({
  page,
}) => {
  const failures: string[] = [];
  page.on("pageerror", (error) => failures.push(error.message));
  await page.goto("/console/mockup.html");
  await page.getByRole("button", { name: "Users", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Platform users", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("columnheader")).toHaveText([
    "User",
    "Created",
    "Applications",
  ]);
  await expect(
    page.getByRole("button", { name: "Load more users", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Load more users", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Load more users", exact: true }),
  ).toHaveCount(0);
  await expect(page.getByText("Loading users…", { exact: true })).toHaveCount(
    0,
  );
  await expect(page.getByRole("table").getByRole("button")).toHaveCount(0);
  await page.screenshot({
    path: ".tmp/platform-users-desktop.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: ".tmp/platform-users-mobile.png",
    fullPage: true,
  });
  const usersWidth = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth,
  }));
  expect(usersWidth.document).toBeLessThanOrEqual(usersWidth.viewport);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("button", { name: "My account", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Google linked", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Link GitHub", exact: true }).click();
  await expect(page.locator(".identity-row")).toHaveCount(2);
  await expect(
    page.getByRole("button", { name: "GitHub linked", exact: true }),
  ).toBeDisabled();
  await page.screenshot({
    path: ".tmp/platform-account-desktop.png",
    fullPage: true,
  });

  await page
    .getByLabel("Preview account", { exact: true })
    .selectOption({ label: "Product developer" });
  await expect(
    page.getByRole("button", { name: "Users", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Manage Syntrix", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Manage Borgee", exact: true }),
  ).toHaveCount(0);
  await page.screenshot({
    path: ".tmp/platform-developer-desktop.png",
    fullPage: true,
  });
  await page.getByLabel("Start signed out", { exact: true }).check();
  for (const provider of ["Microsoft", "Google", "GitHub"])
    await expect(
      page.getByRole("button", {
        name: `Continue with ${provider}`,
        exact: true,
      }),
    ).toBeEnabled();
  await page.screenshot({
    path: ".tmp/platform-login-desktop.png",
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Continue with GitHub", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Manage Syntrix", exact: true }),
  ).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await page
    .getByLabel("Preview account", { exact: true })
    .selectOption({ label: "New account (missing profile)" });
  await page.getByLabel("Start signed out", { exact: true }).uncheck();
  await expect(
    page.getByRole("heading", {
      name: "Your products belong here",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "My account", exact: true }).click();
  await expect(page.locator(".account-profile")).toContainText(
    "Unnamed account",
  );
  await expect(page.locator(".account-profile")).toContainText(
    "Email not provided",
  );
  await expect(page.locator(".identity-row")).toHaveCount(1);
  await page.screenshot({
    path: ".tmp/platform-account-mobile.png",
    fullPage: true,
  });
  await page.getByLabel("Start signed out", { exact: true }).check();
  await expect(
    page.getByRole("button", { name: "Continue with Microsoft", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: ".tmp/platform-login-mobile.png",
    fullPage: true,
  });
  const dimensions = await page.evaluate(() => ({
    viewport: window.innerWidth,
    document: document.documentElement.scrollWidth,
  }));
  expect(dimensions.document).toBeLessThanOrEqual(dimensions.viewport);
  expect(failures).toEqual([]);
});
