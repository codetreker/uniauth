import { spawn, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { resolve } from "node:path";

const executable = resolve(".tmp/bin/uniauth");
const password = "browser-test-administrator-password";
const hashed = spawnSync(executable, ["hash-password"], {
  input: password + "\n",
  encoding: "utf8",
  timeout: 10_000,
});
if (hashed.error !== undefined) throw hashed.error;
if (hashed.status !== 0)
  throw new Error(`Administrator password hashing failed: ${hashed.stderr}`);

mkdirSync(".tmp/browser", { recursive: true });
const directory = mkdtempSync(resolve(".tmp/browser/server-"));
const service = spawn(executable, ["serve"], {
  env: {
    ...process.env,
    // Browsers canonicalize decimal ports before sending an Origin header.
    UNIAUTH_PUBLIC_URL: "http://127.0.0.1:018080",
    UNIAUTH_LISTEN_ADDR: "127.0.0.1:18080",
    UNIAUTH_DATABASE_PATH: resolve(directory, "uniauth.db"),
    UNIAUTH_ADMIN_PASSWORD_HASH: hashed.stdout.trim(),
    UNIAUTH_ALLOW_LOOPBACK_HTTP: "true",
    UNIAUTH_MICROSOFT_CLIENT_ID: "",
    UNIAUTH_MICROSOFT_CLIENT_SECRET: "",
    UNIAUTH_GOOGLE_CLIENT_ID: "",
    UNIAUTH_GOOGLE_CLIENT_SECRET: "",
    UNIAUTH_GITHUB_CLIENT_ID: "",
    UNIAUTH_GITHUB_CLIENT_SECRET: "",
  },
  stdio: ["ignore", "inherit", "inherit"],
});

let stopping = false;
let deadline;
const stop = () => {
  if (stopping) return;
  stopping = true;
  service.kill("SIGTERM");
  deadline = setTimeout(() => service.kill("SIGKILL"), 7_000);
};
process.on("SIGTERM", stop);
process.on("SIGINT", stop);
service.on("error", (error) => {
  console.error(error);
  rmSync(directory, { recursive: true, force: true });
  process.exitCode = 1;
});
service.on("exit", (code, signal) => {
  clearTimeout(deadline);
  rmSync(directory, { recursive: true, force: true });
  process.exitCode = stopping ? 0 : (code ?? (signal === null ? 0 : 1));
});
