import { expect, test } from "@playwright/test";
import type { Locator, Page } from "@playwright/test";

const signIn = async (page: Page, provider: string, persona: string) => {
  await page
    .getByRole("button", { name: `Continue with ${provider}`, exact: true })
    .click();
  await expect(
    page.getByRole("heading", {
      name: `Fixture identity for ${provider.toLowerCase()}`,
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: persona, exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await expect(page).toHaveURL("http://127.0.0.1:18080/console/");
};

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

const createApplication = async (page: Page, name: string) => {
  await page.getByRole("button", { name: /^Applications/ }).click();
  await page
    .getByRole("button", { name: "New application", exact: true })
    .click();
  const editor = page.getByRole("dialog", {
    name: "Create application",
    exact: true,
  });
  await editor.getByLabel("Application name").fill(name);
  await editor.getByLabel("Secret name", { exact: true }).fill("Backend");
  await editor
    .getByLabel("Callback URLs", { exact: true })
    .fill("https://private-product.example/auth/callback");
  await editor.getByRole("checkbox", { name: /Google/ }).check();
  await editor
    .getByRole("button", { name: "Create application", exact: true })
    .click();
  return saveCredential(page);
};

for (const viewport of [
  { name: "desktop", width: 1440, height: 1000 },
  { name: "mobile", width: 390, height: 844 },
]) {
  test(`OAuth sign-in preserves its layout and pending state through navigation on ${viewport.name}`, async ({
    page,
  }) => {
    await page.setViewportSize(viewport);
    await page.goto("/console/");
    const google = page.getByRole("button", {
      name: "Continue with Google",
      exact: true,
    });
    await expect(google).toBeEnabled();
    await page.evaluate(() => document.fonts.ready);
    await google.scrollIntoViewIfNeeded();
    const geometry = () =>
      page.locator(".login-card, .oauth-button").evaluateAll((elements) =>
        elements.map((element) => {
          const { x, y, width, height } = element.getBoundingClientRect();
          return { x, y, width, height };
        }),
      );
    const original = await geometry();
    const returnProbe = new URL("#oauth-start-pending", page.url()).href;
    let authorizationURL = "";
    const firstStart = Promise.withResolvers<void>();
    const releaseFailure = Promise.withResolvers<void>();
    const retryStart = Promise.withResolvers<void>();
    const releaseSuccess = Promise.withResolvers<void>();
    let starts = 0;
    await page.route("**/console/api/login", async (route) => {
      starts += 1;
      if (starts === 1) {
        firstStart.resolve();
        await releaseFailure.promise;
        await route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            error: {
              code: "service_unavailable",
              message: "Sign-in temporarily unavailable.",
            },
          }),
        });
      } else if (starts === 2) {
        try {
          const response = await route.fetch();
          expect(response.ok()).toBe(true);
          authorizationURL = (await response.json()).authorization_url;
          retryStart.resolve();
          await releaseSuccess.promise;
          // A same-document destination keeps the real assign-return state observable.
          await route.fulfill({
            response,
            json: { authorization_url: returnProbe },
          });
        } catch (error) {
          retryStart.reject(error);
          throw error;
        }
      } else {
        await route.continue();
      }
    });
    const expectPending = async (expectedStarts: number) => {
      await expect(
        page.getByRole("button", { name: "Connecting…", exact: true }),
      ).toBeDisabled();
      await expect(page.locator(".oauth-button:disabled")).toHaveCount(3);
      await expect(
        page.getByRole("button", {
          name: "Continue with Microsoft",
          exact: true,
        }),
      ).toBeDisabled();
      await expect(
        page.getByRole("button", { name: "Continue with GitHub", exact: true }),
      ).toBeDisabled();
      await expect(
        page.getByText("Finish the pending operation", { exact: false }),
      ).toHaveCount(0);
      expect(await geometry()).toEqual(original);
      expect(starts).toBe(expectedStarts);
    };
    try {
      await google.evaluate((button) => (button as HTMLButtonElement).click());
      await firstStart.promise;
      await expectPending(1);
      await page.locator(".oauth-button").evaluateAll((buttons) => {
        for (const button of buttons) (button as HTMLButtonElement).click();
      });
      expect(starts).toBe(1);
      releaseFailure.resolve();
      await expect(page.getByRole("alert")).toContainText(
        "Sign-in temporarily unavailable.",
      );
      await expect(google).toBeEnabled();
      await expect(page.locator(".oauth-button:disabled")).toHaveCount(0);

      await google.evaluate((button) => (button as HTMLButtonElement).click());
      await retryStart.promise;
      await expect(page.getByRole("alert")).toHaveCount(0);
      await expectPending(2);
      await page.locator(".oauth-button").evaluateAll((buttons) => {
        for (const button of buttons) (button as HTMLButtonElement).click();
      });
      expect(starts).toBe(2);
      releaseSuccess.resolve();
      await expect(page).toHaveURL(returnProbe);
      await expectPending(2);
      await page.screenshot({
        path: `.tmp/oauth-recovery-${viewport.name}.png`,
        fullPage: true,
      });
      await page.locator(".oauth-button").evaluateAll((buttons) => {
        for (const button of buttons) (button as HTMLButtonElement).click();
      });
      expect(starts).toBe(2);
      await page.goto(authorizationURL);
      await expect(
        page.getByRole("heading", {
          name: "Fixture identity for google",
          exact: true,
        }),
      ).toBeVisible();
      await page.goBack();
      await expect(google).toBeEnabled();
      await expect(page.locator(".oauth-button:disabled")).toHaveCount(0);
      await page
        .getByRole("button", { name: "Continue with Microsoft", exact: true })
        .click();
      await expect(
        page.getByRole("heading", {
          name: "Fixture identity for microsoft",
          exact: true,
        }),
      ).toBeVisible();
      expect(starts).toBe(3);
    } finally {
      releaseFailure.resolve();
      releaseSuccess.resolve();
    }
  });
}

test("identity linking holds its account controls and list until external navigation", async ({
  page,
}) => {
  await page.goto("/console/");
  await signIn(page, "Microsoft", "Identity without email");
  await page.getByRole("button", { name: "My account", exact: true }).click();
  await expect(page.locator(".identity-row")).toHaveCount(1);
  const identities = await page.locator(".identity-list").innerText();
  const returnProbe = new URL("#oauth-link-pending", page.url()).href;
  let authorizationURL = "";
  let identityReloads = 0;
  let starts = 0;
  page.on("request", (request) => {
    if (
      request.method() === "GET" &&
      request.url().endsWith("/console/api/identities")
    )
      identityReloads += 1;
  });
  const apiStarted = Promise.withResolvers<void>();
  const releaseAPI = Promise.withResolvers<void>();
  await page.route("**/console/api/identities/link", async (route) => {
    starts += 1;
    try {
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      authorizationURL = (await response.json()).authorization_url;
      apiStarted.resolve();
      await releaseAPI.promise;
      await route.fulfill({
        response,
        json: { authorization_url: returnProbe },
      });
    } catch (error) {
      apiStarted.reject(error);
      throw error;
    }
  });
  const expectPending = async () => {
    await expect(
      page.getByRole("button", { name: "Connecting…", exact: true }),
    ).toBeDisabled();
    await expect(page.locator(".link-providers button:disabled")).toHaveCount(
      3,
    );
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeDisabled();
    expect(await page.locator(".identity-list").innerText()).toBe(identities);
    await expect(
      page.getByText("Loading linked identities…", { exact: true }),
    ).toHaveCount(0);
    expect(starts).toBe(1);
    expect(identityReloads).toBe(0);
  };
  try {
    await page
      .getByRole("button", { name: "Link GitHub", exact: true })
      .evaluate((button) => (button as HTMLButtonElement).click());
    await apiStarted.promise;
    await expectPending();
    await page.locator(".link-providers button").evaluateAll((buttons) => {
      for (const button of buttons) (button as HTMLButtonElement).click();
    });
    expect(starts).toBe(1);
    releaseAPI.resolve();
    await expect(page).toHaveURL(returnProbe);
    await expectPending();
    await page.locator(".link-providers button").evaluateAll((buttons) => {
      for (const button of buttons) (button as HTMLButtonElement).click();
    });
    expect(starts).toBe(1);
    await page.goto(authorizationURL);
    await expect(
      page.getByRole("heading", {
        name: "Fixture identity for github",
        exact: true,
      }),
    ).toBeVisible();
    await page.goBack();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeEnabled();
    await page.getByRole("button", { name: "My account", exact: true }).click();
    await expect(
      page.getByRole("button", { name: "Link GitHub", exact: true }),
    ).toBeEnabled();
    await expect(page.locator(".identity-row")).toHaveCount(1);
  } finally {
    releaseAPI.resolve();
  }
});

for (const operation of ["login", "link"] as const) {
  for (const recovery of ["automatic", "manual"] as const) {
    test(`cancelled OAuth ${operation} navigation restores controls with ${recovery} recovery`, async ({
      page,
    }) => {
      if (recovery === "manual") {
        await page.addInitScript(() => {
          Object.defineProperty(window, "navigation", {
            configurable: true,
            value: undefined,
          });
        });
      }
      await page.goto("/console/");
      if (operation === "link") {
        await signIn(page, "Microsoft", "Identity without email");
        await page
          .getByRole("button", { name: "My account", exact: true })
          .click();
        await expect(page.locator(".identity-row")).toHaveCount(1);
      }
      let starts = 0;
      page.on("request", (request) => {
        if (
          request.method() === "POST" &&
          request
            .url()
            .endsWith(
              operation === "login"
                ? "/console/api/login"
                : "/console/api/identities/link",
            )
        )
          starts += 1;
      });
      const originalURL = page.url();
      await page.evaluate(() => {
        document.documentElement.dataset.navigationProbe = "original-document";
      });
      const authorizationStarted = Promise.withResolvers<void>();
      const releaseAuthorization = Promise.withResolvers<void>();
      const authorizationReleased = Promise.withResolvers<void>();
      const authorizationRoute = "**/auth/authorize?**";
      await page.route(authorizationRoute, async (route) => {
        authorizationStarted.resolve();
        try {
          await releaseAuthorization.promise;
          await route.continue();
          authorizationReleased.resolve();
        } catch (error) {
          authorizationReleased.reject(error);
        }
      });
      const recoveryPosition = Promise.withResolvers<{
        x: number;
        y: number;
      }>();
      if (recovery === "manual") {
        await page.exposeFunction(
          "reportOAuthRecovery",
          (position: { x: number; y: number }) =>
            recoveryPosition.resolve(position),
        );
        await page.evaluate((operation) => {
          // Observe the original document while Playwright waits for navigation.
          const observer = new MutationObserver(() => {
            const button = [
              ...document.querySelectorAll<HTMLButtonElement>("button"),
            ].find((candidate) => candidate.textContent === "Cancel sign-in");
            if (button === undefined) return;
            observer.disconnect();
            const providers = document.querySelectorAll<HTMLButtonElement>(
              operation === "login"
                ? ".oauth-button"
                : ".link-providers button",
            );
            for (const provider of providers) provider.click();
            const { x, y, width, height } = button.getBoundingClientRect();
            void Reflect.get(
              window,
              "reportOAuthRecovery",
            )({
              x: x + width / 2,
              y: y + height / 2,
            });
          });
          observer.observe(document.body, { childList: true, subtree: true });
        }, operation);
      }
      const cancelledNavigation = page.waitForEvent(
        "requestfailed",
        (request) => new URL(request.url()).pathname === "/auth/authorize",
      );
      const protocol = await page.context().newCDPSession(page);
      try {
        const startName =
          operation === "login" ? "Continue with Google" : "Link GitHub";
        await page
          .getByRole("button", { name: startName, exact: true })
          .evaluate((button) => (button as HTMLButtonElement).click());
        await authorizationStarted.promise;
        if (recovery === "manual") {
          const position = await recoveryPosition.promise;
          expect(starts).toBe(1);
          for (const type of ["mousePressed", "mouseReleased"]) {
            await protocol.send("Input.dispatchMouseEvent", {
              type,
              ...position,
              button: "left",
              clickCount: 1,
            });
          }
        } else {
          await protocol.send("Page.stopLoading");
        }
        expect((await cancelledNavigation).failure()?.errorText).toBe(
          "net::ERR_ABORTED",
        );
        releaseAuthorization.resolve();
        await authorizationReleased.promise;
        await expect(page).toHaveURL(originalURL);
        await expect(page.locator("html")).toHaveAttribute(
          "data-navigation-probe",
          "original-document",
        );
        expect(starts).toBe(1);
        await expect(
          page.getByRole("button", { name: startName, exact: true }),
        ).toBeEnabled();
        await expect(
          page.getByRole("button", { name: "Connecting…", exact: true }),
        ).toHaveCount(0);
        if (operation === "link") {
          await expect(
            page.getByRole("button", { name: "Sign out", exact: true }),
          ).toBeEnabled();
          await expect(page.locator(".identity-row")).toHaveCount(1);
          await page.getByRole("button", { name: /^Applications/ }).click();
          await page
            .getByRole("button", { name: "My account", exact: true })
            .click();
        } else {
          await expect(page.locator(".oauth-button:disabled")).toHaveCount(0);
        }
      } finally {
        releaseAuthorization.resolve();
        await page.unroute(authorizationRoute);
        await protocol.detach();
      }
      await page
        .getByRole("button", {
          name:
            operation === "login" ? "Continue with Microsoft" : "Link Google",
          exact: true,
        })
        .click();
      await expect(
        page.getByRole("heading", {
          name: `Fixture identity for ${operation === "login" ? "microsoft" : "google"}`,
          exact: true,
        }),
      ).toBeVisible();
      expect(starts).toBe(2);
    });
  }
}

test("OAuth account manages applications and one-time credentials against the service", async ({
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
  for (const provider of ["Microsoft", "Google", "GitHub"]) {
    await expect(
      page.getByRole("button", {
        name: `Continue with ${provider}`,
        exact: true,
      }),
    ).toBeEnabled();
  }
  await page.screenshot({
    path: ".tmp/platform-login-real.png",
    fullPage: true,
  });
  await signIn(page, "Google", "Administrator");
  await expect(
    page.getByRole("heading", { name: "Your products belong here" }),
  ).toBeVisible();

  const cookies = await context.cookies();
  expect(cookies).toHaveLength(1);
  expect(cookies[0]).toMatchObject({
    name: "uniauth_console",
    httpOnly: true,
    sameSite: "Strict",
    secure: false,
    path: "/",
  });
  expect(await page.evaluate(() => document.cookie)).not.toContain(
    "uniauth_console",
  );
  const anonymous = await browser.newContext();
  try {
    const anonymousPage = await anonymous.newPage();
    await anonymousPage.goto("http://127.0.0.1:18080/console/");
    await expect(
      anonymousPage.getByRole("button", {
        name: "Continue with Google",
        exact: true,
      }),
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
    page.locator(".provider-card").getByText("Configured", { exact: true }),
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
    page.locator(".configuration .provider-pills > span"),
  ).toHaveCount(3);
  await expect(
    page.locator(".configuration").getByText("Not configured", { exact: true }),
  ).toHaveCount(0);
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
  ).toHaveCount(0);
  await expect(
    page.getByRole("row").filter({ hasText: resetCredential.id }),
  ).toContainText("Active");
  await page.getByRole("tab", { name: /^Disabled/ }).click();
  await expect(
    page.getByRole("row").filter({ hasText: replacement.id }),
  ).toContainText("Revoked");
  await page.getByRole("tab", { name: /^Active/ }).click();
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
  await expect(page.getByRole("row").filter({ hasText: first.id })).toHaveCount(
    0,
  );
  await expect(
    page.getByRole("row").filter({ hasText: resetCredential.id }),
  ).toContainText("Active");
  await page.getByRole("tab", { name: /^Disabled/ }).click();
  await expect(
    page.getByRole("row").filter({ hasText: first.id }),
  ).toContainText("Revoked");
  await expect(
    page.getByRole("row").filter({ hasText: replacement.id }),
  ).toContainText("Revoked");
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
  await expect(page.getByRole("tab", { name: /^Active/ })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(
    page.getByRole("row").filter({ hasText: resetCredential.id }),
  ).toContainText("Active");
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
  await page.getByRole("tab", { name: /^Disabled/ }).click();
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
  await expect(
    page.getByRole("button", { name: "Continue with Google", exact: true }),
  ).toBeVisible();
  expect((await context.request.get("/console/api/session")).status()).toBe(
    401,
  );
  expect(await context.cookies()).toHaveLength(0);
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Continue with Google", exact: true }),
  ).toBeVisible();
  expect(failures).toEqual([]);
});

test("public accounts own their Apps and administrators receive only user summaries", async ({
  browser,
}) => {
  const alice = await browser.newContext({ baseURL: "http://127.0.0.1:18080" });
  const bob = await browser.newContext({ baseURL: "http://127.0.0.1:18080" });
  const administrator = await browser.newContext({
    baseURL: "http://127.0.0.1:18080",
  });
  try {
    const alicePage = await alice.newPage();
    const bobPage = await bob.newPage();
    const adminPage = await administrator.newPage();
    for (const page of [alicePage, bobPage, adminPage])
      await page.goto("/console/");
    await signIn(alicePage, "Google", "User A");
    await signIn(bobPage, "GitHub", "User B");
    const aliceAccount = (
      await (await alice.request.get("/console/api/session")).json()
    ).user;
    const bobAccount = (
      await (await bob.request.get("/console/api/session")).json()
    ).user;
    expect(aliceAccount.is_admin).toBe(false);
    expect(bobAccount.is_admin).toBe(false);
    expect(aliceAccount.id).not.toBe(bobAccount.id);
    for (const page of [alicePage, bobPage])
      await expect(
        page.getByRole("button", { name: "Users", exact: true }),
      ).toHaveCount(0);
    const aliceApp = await createApplication(
      alicePage,
      "Alice private product",
    );
    const bobApp = await createApplication(bobPage, "Bob private product");
    await alicePage.screenshot({
      path: ".tmp/platform-application-real.png",
      fullPage: true,
    });
    expect(
      (await (await alice.request.get("/console/api/apps")).json()).apps.map(
        (app: { id: string }) => app.id,
      ),
    ).toEqual([aliceApp.appID]);
    expect(
      (await (await bob.request.get("/console/api/apps")).json()).apps.map(
        (app: { id: string }) => app.id,
      ),
    ).toEqual([bobApp.appID]);
    expect((await alice.request.get("/console/api/users")).status()).toBe(403);
    await signIn(adminPage, "Google", "Administrator");
    expect(
      (await (await administrator.request.get("/console/api/session")).json())
        .user.is_admin,
    ).toBe(true);
    const adminApps = (
      await (await administrator.request.get("/console/api/apps")).json()
    ).apps;
    expect(
      adminApps.some(
        (app: { id: string }) =>
          app.id === aliceApp.appID || app.id === bobApp.appID,
      ),
    ).toBe(false);
    for (const actor of [bob, administrator]) {
      for (const path of [
        `/console/api/apps/${aliceApp.appID}`,
        `/console/api/apps/${aliceApp.appID}/secrets`,
      ]) {
        const response = await actor.request.get(path);
        expect(response.status()).toBe(404);
        expect(await response.text()).not.toContain("Alice private product");
      }
      const session = await (
        await actor.request.get("/console/api/session")
      ).json();
      const headers = {
        Origin: "http://127.0.0.1:18080",
        "X-CSRF-Token": session.csrf_token,
      };
      const updates = [
        await actor.request.put(`/console/api/apps/${aliceApp.appID}`, {
          headers,
          data: {
            name: "Stolen",
            enabled: true,
            providers: ["google"],
            redirect_uris: ["https://stolen.example/callback"],
          },
        }),
        await actor.request.delete(`/console/api/apps/${aliceApp.appID}`, {
          headers,
        }),
        await actor.request.post(
          `/console/api/apps/${aliceApp.appID}/secrets`,
          { headers, data: { name: "Stolen", description: "" } },
        ),
        await actor.request.put(
          `/console/api/apps/${aliceApp.appID}/secrets/${aliceApp.id}`,
          { headers, data: { name: "Stolen", description: "" } },
        ),
        await actor.request.delete(
          `/console/api/apps/${aliceApp.appID}/secrets/${aliceApp.id}`,
          { headers },
        ),
        await actor.request.post(
          `/console/api/apps/${aliceApp.appID}/secrets/${aliceApp.id}/reset`,
          { headers },
        ),
      ];
      for (const response of updates) expect(response.status()).toBe(404);
    }
    const summary = await (
      await administrator.request.get("/console/api/users")
    ).json();
    for (const user of summary.users)
      expect(Object.keys(user).sort()).toEqual([
        "app_count",
        "created_at",
        "email",
        "id",
        "name",
      ]);
    expect(
      summary.users.find((user: { id: string }) => user.id === aliceAccount.id)
        .app_count,
    ).toBe(1);
    expect(
      summary.users.find((user: { id: string }) => user.id === bobAccount.id)
        .app_count,
    ).toBe(1);
    expect(JSON.stringify(summary)).not.toContain(aliceApp.appID);
    expect(JSON.stringify(summary)).not.toContain(aliceApp.secret);
    expect(JSON.stringify(summary)).not.toContain("private-product.example");
    await adminPage.getByRole("button", { name: "Users", exact: true }).click();
    await expect(
      adminPage.getByRole("heading", { name: "Platform users", exact: true }),
    ).toBeVisible();
    await expect(
      adminPage
        .getByRole("row")
        .filter({ hasText: aliceAccount.id })
        .locator(".user-app-count"),
    ).toHaveText("1");
    await expect(
      adminPage
        .getByRole("row")
        .filter({ hasText: bobAccount.id })
        .getByRole("button"),
    ).toHaveCount(0);
    await adminPage.screenshot({
      path: ".tmp/platform-users-real.png",
      fullPage: true,
    });

    await alicePage
      .getByRole("button", { name: "My account", exact: true })
      .click();
    await alicePage
      .getByRole("button", { name: "Link GitHub", exact: true })
      .click();
    await alicePage
      .getByRole("button", { name: "User B", exact: true })
      .click();
    await expect(alicePage.getByRole("alert")).toContainText(
      "belongs to another account",
    );
    expect(
      (await (await alice.request.get("/console/api/session")).json()).user.id,
    ).toBe(aliceAccount.id);
    expect(
      (await (await alice.request.get("/console/api/identities")).json())
        .identities,
    ).toHaveLength(1);
    await alicePage
      .getByRole("button", { name: "My account", exact: true })
      .click();
    await alicePage
      .getByRole("button", { name: "Link GitHub", exact: true })
      .click();
    await alicePage
      .getByRole("button", { name: "User A", exact: true })
      .click();
    await alicePage
      .getByRole("button", { name: "My account", exact: true })
      .click();
    await expect(alicePage.locator(".identity-row")).toHaveCount(2);
    await expect(
      alicePage.getByRole("button", { name: "GitHub linked", exact: true }),
    ).toBeDisabled();
    await alicePage.screenshot({
      path: ".tmp/platform-account-real.png",
      fullPage: true,
    });
    await alicePage
      .getByRole("button", { name: "Sign out", exact: true })
      .click();
    await signIn(alicePage, "GitHub", "User A");
    expect(
      (await (await alice.request.get("/console/api/session")).json()).user.id,
    ).toBe(aliceAccount.id);
    await expect(
      alicePage.getByRole("button", {
        name: "Manage Alice private product",
        exact: true,
      }),
    ).toBeVisible();
    await alicePage
      .getByRole("button", { name: "Sign out", exact: true })
      .click();
    await signIn(alicePage, "Google", "Separate identity with same email");
    const separate = (
      await (await alice.request.get("/console/api/session")).json()
    ).user;
    expect(separate.email).toBe(aliceAccount.email);
    expect(separate.id).not.toBe(aliceAccount.id);
    expect(
      (await (await alice.request.get("/console/api/apps")).json()).apps,
    ).toEqual([]);
  } finally {
    await Promise.all([alice.close(), bob.close(), administrator.close()]);
  }
});

test("Microsoft login supports missing email and cannot claim admin through unverified email", async ({
  page,
  context,
}) => {
  await page.goto("/console/");
  await signIn(page, "Microsoft", "Administrator");
  let user = (await (await context.request.get("/console/api/session")).json())
    .user;
  expect(user.email).toBe("sychenjianjun@gmail.com");
  expect(user.is_admin).toBe(false);
  await expect(
    page.getByRole("button", { name: "Users", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await signIn(page, "Microsoft", "Identity without email");
  user = (await (await context.request.get("/console/api/session")).json())
    .user;
  expect(user.email).toBeNull();
  expect(user.is_admin).toBe(false);
  await page.getByRole("button", { name: "My account", exact: true }).click();
  await expect(page.locator(".account-profile")).toContainText(
    "Email not provided",
  );
  await expect(page.locator(".identity-row")).toContainText(
    "Verification not provided",
  );
});

test("OAuth handover requires its initiating browser and cannot be replayed or completed after logout", async ({
  page,
  context,
  browser,
}) => {
  await page.goto("/console/");
  expect(
    (await (await context.request.get("/console/api/providers")).json())
      .providers,
  ).toEqual([
    { id: "microsoft", available: true },
    { id: "google", available: true },
    { id: "github", available: true },
  ]);
  expect(
    (
      await context.request.post("/console/api/login", {
        headers: { Origin: "http://127.0.0.1:18080" },
        data: { password: "removed-password" },
      })
    ).status(),
  ).toBe(400);
  await page
    .getByRole("button", { name: "Continue with GitHub", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Cancel provider login", exact: true })
    .click();
  await expect(page.getByRole("alert")).toContainText("provider");
  expect((await context.request.get("/console/api/session")).status()).toBe(
    401,
  );

  let callback = "";
  const pauseProvider = /\/fixture\/oauth\/google(?:\?.*)?$/;
  await page.route(pauseProvider, async (route) => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    // Stop the redirect chain before Console consumes its browser-bound handover.
    const chooser = await route.fetch({ maxRedirects: 0 });
    expect(chooser.status()).toBe(303);
    const response = await context.request.get(chooser.headers().location, {
      maxRedirects: 0,
    });
    expect(response.status()).toBe(302);
    callback = response.headers().location;
    expect(callback).toContain("/console/auth/callback?");
    await route.fulfill({
      status: 200,
      contentType: "text/html",
      body: "<h1>Paused Console callback</h1>",
    });
  });
  await page
    .getByRole("button", { name: "Continue with Google", exact: true })
    .click();
  await page.getByRole("button", { name: "User A", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Paused Console callback" }),
  ).toBeVisible();
  const stranger = await browser.newContext();
  try {
    const strangerPage = await stranger.newPage();
    await strangerPage.goto(callback);
    await expect(
      strangerPage.getByRole("button", {
        name: "Continue with Google",
        exact: true,
      }),
    ).toBeVisible();
    expect(
      (
        await stranger.request.get("http://127.0.0.1:18080/console/api/session")
      ).status(),
    ).toBe(401);
  } finally {
    await stranger.close();
  }
  await page.unroute(pauseProvider);
  await page.goto(callback);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const before = (await context.cookies()).find(
    (cookie) => cookie.name === "uniauth_console",
  )?.value;
  expect(before).toHaveLength(43);
  await page.goto(callback);
  await expect(page.getByRole("alert")).toContainText("already used");
  expect(
    (await context.cookies()).find(
      (cookie) => cookie.name === "uniauth_console",
    )?.value,
  ).toBe(before);

  const session = await (
    await context.request.get("/console/api/session")
  ).json();
  const headers = {
    Origin: "http://127.0.0.1:18080",
    "X-CSRF-Token": session.csrf_token,
  };
  const link = await (
    await context.request.post("/console/api/identities/link", {
      headers,
      data: { provider: "microsoft" },
    })
  ).json();
  await page.goto(link.authorization_url);
  expect(
    (await context.request.post("/console/api/logout", { headers })).status(),
  ).toBe(204);
  await page.getByRole("button", { name: "User A", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Continue with Google", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("alert")).toContainText("already used");
  expect((await context.request.get("/console/api/session")).status()).toBe(
    401,
  );
  await signIn(page, "Google", "User A");
  const identities = (
    await (await context.request.get("/console/api/identities")).json()
  ).identities;
  expect(
    identities.some(
      (identity: { provider: string }) => identity.provider === "microsoft",
    ),
  ).toBe(false);
});
