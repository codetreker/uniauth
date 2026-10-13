import { spawn } from "node:child_process";
import { mkdtemp, mkdir, readdir, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const packageRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const repositoryRoot = resolve(packageRoot, "../..");
const temporaryRoot = join(repositoryRoot, ".tmp");
await mkdir(temporaryRoot, { recursive: true });
const temporary = await mkdtemp(join(temporaryRoot, "sdk-js-consumer-"));
const environment = {
  ...process.env,
  XDG_CACHE_HOME: join(temporaryRoot, "pnpm-cache"),
  XDG_DATA_HOME: join(temporaryRoot, "pnpm-data"),
  TMPDIR: temporaryRoot,
};

const run = async (command, arguments_, cwd) => {
  const child = spawn(command, arguments_, {
    cwd,
    env: environment,
    stdio: "inherit",
  });
  let timedOut = false;
  const timeout = setTimeout(() => {
    timedOut = true;
    child.kill("SIGTERM");
  }, 120_000);
  const forceStop = setTimeout(() => child.kill("SIGKILL"), 125_000);
  try {
    await new Promise((resolve_, reject) => {
      child.once("error", reject);
      child.once("close", (code, signal) => {
        if (code === 0 && !timedOut) resolve_();
        else
          reject(
            new Error(
              `${command} failed (${timedOut ? "timeout" : (signal ?? code)}).`,
            ),
          );
      });
    });
  } finally {
    clearTimeout(timeout);
    clearTimeout(forceStop);
  }
};

try {
  const packed = join(temporary, "packed");
  const consumer = join(temporary, "consumer");
  await mkdir(packed);
  await mkdir(consumer);
  await run("pnpm", ["pack", "--pack-destination", packed], packageRoot);
  const archives = (await readdir(packed)).filter((file) =>
    file.endsWith(".tgz"),
  );
  if (archives.length !== 1)
    throw new Error("Expected exactly one packed SDK archive.");
  await writeFile(
    join(consumer, "package.json"),
    JSON.stringify({
      name: "uniauth-sdk-consumer-check",
      private: true,
      type: "module",
      dependencies: { "@codetreker/uniauth": `file:../packed/${archives[0]}` },
    }),
  );
  await writeFile(
    join(consumer, "tsconfig.json"),
    JSON.stringify({
      compilerOptions: {
        target: "ES2024",
        module: "NodeNext",
        moduleResolution: "NodeNext",
        lib: ["ES2024"],
        types: ["node"],
        typeRoots: [join(packageRoot, "node_modules", "@types")],
        strict: true,
        exactOptionalPropertyTypes: true,
        skipLibCheck: false,
        noEmit: true,
      },
      include: ["consumer.ts"],
    }),
  );
  await writeFile(
    join(consumer, "consumer.ts"),
    `import { APIError, CallbackError, ProtocolError, createClient, generateState, parseCallback } from "@codetreker/uniauth";
import type { Client, Identity, Provider } from "@codetreker/uniauth";
const provider: Provider = "github";
const client: Client = createClient({ baseURL: "https://auth.example", appID: "product", appSecret: "secret" });
const state: string = generateState();
const url: string = client.authorizationURL({ provider, redirectURI: "https://product.example/callback", state });
const code: string = parseCallback(new URLSearchParams({ state, code: "code" }), state);
const result: Promise<Identity> = client.retrieve(code, { signal: new AbortController().signal });
const errors: readonly Error[] = [new APIError(410, "result_unavailable", "Unavailable.", null), new CallbackError("state_mismatch"), new ProtocolError("Invalid JSON.", 200, null)];
void [url, result, errors];
`,
  );
  await writeFile(
    join(consumer, "consumer.mjs"),
    `import assert from "node:assert/strict";
import { realpath } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createClient, generateState, parseCallback } from "@codetreker/uniauth";
assert.equal(await realpath(fileURLToPath(import.meta.resolve("@codetreker/uniauth"))), await realpath(fileURLToPath(new URL("./node_modules/@codetreker/uniauth/dist/index.js", import.meta.url))));
const rawJSON = '{ "provider":"github", "subject":"github:1", "sources":{"future":false,"large":9007199254740993} }';
const client = createClient({ baseURL: "https://auth.example", appID: "product", appSecret: "secret", fetch: async () => new Response(rawJSON, { headers: { "Content-Type": "application/json" } }) });
const state = generateState();
assert.equal(parseCallback(new URLSearchParams({ state, code: "code" }), state), "code");
assert.equal(new URL(client.authorizationURL({ provider: "github", redirectURI: "https://product.example/callback", state })).searchParams.get("state"), state);
const identity = await client.retrieve("code");
assert.equal(identity.rawJSON, rawJSON);
assert.equal(identity.sources.future, false);
console.log("Packed SDK ESM consumer passed.");
`,
  );
  await run(
    "pnpm",
    [
      "install",
      "--offline",
      "--ignore-scripts",
      "--no-frozen-lockfile",
      "--store-dir",
      join(temporaryRoot, "pnpm-store"),
    ],
    consumer,
  );
  // The consumer owns SDK resolution. Only the pinned compiler and Node ambient
  // types come from frozen setup, avoiding a second registry-dependent toolchain.
  await run(
    "pnpm",
    ["exec", "tsc", "-p", join(consumer, "tsconfig.json")],
    packageRoot,
  );
  await run("node", ["consumer.mjs"], consumer);
} finally {
  await rm(temporary, { recursive: true, force: true });
}
