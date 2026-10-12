import { defineConfig } from "@playwright/test";

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;

export default defineConfig({
  testDir: "./e2e",
  timeout: 45_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  outputDir: ".tmp/browser/results",
  reporter: [["list"]],
  use: {
    browserName: "chromium",
    viewport: { width: 1440, height: 1000 },
    launchOptions: executablePath === undefined ? {} : { executablePath },
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  projects: [
    {
      name: "console",
      testMatch: "console.spec.ts",
      use: { baseURL: "http://127.0.0.1:18080" },
    },
    {
      name: "mockup",
      testMatch: "mockup.spec.ts",
      use: { baseURL: "http://127.0.0.1:5173" },
    },
  ],
  webServer: [
    {
      command: "node e2e/start-service.mjs",
      url: "http://127.0.0.1:18080/readyz",
      reuseExistingServer: false,
      timeout: 30_000,
      gracefulShutdown: { signal: "SIGTERM", timeout: 10_000 },
      stdout: "pipe",
      stderr: "pipe",
    },
    {
      command:
        "pnpm --dir web/console exec vite --host 127.0.0.1 --port 5173 --strictPort",
      url: "http://127.0.0.1:5173/console/mockup.html",
      reuseExistingServer: false,
      timeout: 30_000,
      gracefulShutdown: { signal: "SIGTERM", timeout: 5_000 },
      stdout: "pipe",
      stderr: "pipe",
    },
  ],
});
