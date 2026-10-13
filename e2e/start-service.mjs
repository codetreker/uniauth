import { spawn, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { resolve } from "node:path";

const executable = resolve(".tmp/bin/uniauth-browser");
for (const directory of ["browser", "bin", "go-tmp", "tmp"])
  mkdirSync(resolve(".tmp", directory), { recursive: true });
const env = {
  ...process.env,
  GOWORK: "off",
  GOCACHE: resolve(".tmp/go-build"),
  GOMODCACHE: resolve(".tmp/go-mod"),
  GOPATH: resolve(".tmp/go"),
  GOTMPDIR: resolve(".tmp/go-tmp"),
  TMPDIR: resolve(".tmp/tmp"),
};
const build = spawnSync(
  "go",
  ["build", "-trimpath", "-o", executable, "./cmd"],
  {
    cwd: resolve("e2e/browser"),
    env,
    stdio: "inherit",
    timeout: 120_000,
  },
);
if (build.error !== undefined) throw build.error;
if (build.status !== 0) process.exit(build.status ?? 1);
const directory = mkdtempSync(resolve(".tmp/browser/server-"));
const service = spawn(executable, [], {
  env: {
    ...env,
    UNIAUTH_DATABASE_PATH: resolve(directory, "uniauth.db"),
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
  process.exitCode = code ?? (signal === null ? 0 : 1);
});
