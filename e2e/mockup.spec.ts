import { expect, test } from "@playwright/test";

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
    page.getByRole("row").filter({ hasText: "sec_0_previous" }),
  ).toContainText("Revoked");
  await expect(
    page.getByRole("row").filter({ hasText: "sec_0_current" }),
  ).toContainText("Production backend");
  await expect(
    page.getByRole("row").filter({ hasText: "sec_0_staging" }),
  ).toContainText("No description");
  await page.screenshot({ path: ".tmp/mockup-detail.png", fullPage: true });
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
  await expect(
    page.getByRole("row").filter({ hasText: "sec_0_previous" }),
  ).toContainText("Retained deployment history");
  await page.getByRole("button", { name: "← All applications" }).click();
  await page.getByRole("button", { name: "New application" }).click();
  const editor = page.getByRole("dialog", {
    name: "Create application",
    exact: true,
  });
  await editor.getByLabel("Application name").fill("New workspace");
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
  await page.getByRole("button", { name: "Try the login screen" }).click();
  await expect(page.getByLabel("Administrator password")).toBeVisible();
  await page.getByLabel("Administrator password").fill("preview");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
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
  await expect(
    page.getByRole("row").filter({ hasText: "sec_2_unicode" }),
  ).toContainText("开发环境");
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
