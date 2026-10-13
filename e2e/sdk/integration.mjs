import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  APIError,
  CallbackError,
  createClient,
  generateState,
  parseCallback,
} from "../../sdk/js/dist/index.js";

const fixture = JSON.parse(readFileSync(0, "utf8"));
const clientFor = ({ app, secret }) =>
  createClient({
    baseURL: fixture.baseURL,
    appID: app.id,
    appSecret: secret,
    allowLoopbackHTTP: true,
    timeoutMs: 5_000,
  });
const client = clientFor(fixture.app);
const browserFetch = (url, options = {}) =>
  fetch(url, {
    ...options,
    redirect: "manual",
    signal: AbortSignal.timeout(5_000),
  });

const login = async () => {
  const state = generateState();
  const authorization = client.authorizationURL({
    provider: "google",
    redirectURI: fixture.redirectURI,
    state,
  });
  const started = await browserFetch(authorization);
  await started.arrayBuffer();
  assert.equal(started.status, 302);
  const upstream = new URL(started.headers.get("Location"));
  const cookies = started.headers.getSetCookie();
  assert.equal(cookies.length, 1);
  assert.match(cookies[0], /HttpOnly/);
  assert.notEqual(upstream.searchParams.get("state"), state);

  const callback = new URL("/auth/callback/google", fixture.baseURL);
  callback.searchParams.set("state", upstream.searchParams.get("state"));
  callback.searchParams.set("code", upstream.searchParams.get("state"));
  const completed = await browserFetch(callback, {
    headers: { Cookie: cookies[0].split(";", 1)[0] },
  });
  await completed.arrayBuffer();
  assert.equal(completed.status, 302);
  const product = new URL(completed.headers.get("Location"));
  assert.equal(product.origin, "https://product.example");
  assert.equal(product.searchParams.get("existing"), "preserved");
  assert.throws(
    () => parseCallback(product.searchParams, "incorrect-state"),
    (error) =>
      error instanceof CallbackError && error.code === "state_mismatch",
  );
  return parseCallback(product.searchParams, state);
};

const apiError = (status, code) => (error) => {
  assert.ok(error instanceof APIError);
  assert.equal(error.status, status);
  assert.equal(error.code, code);
  assert.ok(error.requestID);
  return true;
};

const identityMatches = (identity) => {
  assert.equal(identity.provider, "google");
  assert.equal(identity.id, "integration-user");
  assert.equal(identity.email, null);
  assert.equal(identity.email_verified, false);
  assert.equal(identity.name, "Integration User");
  assert.equal(identity.avatar_url, null);
  assert.deepEqual(identity.origin, {
    profile: JSON.parse(fixture.profileJSON),
    optional: null,
    enabled: false,
  });
  assert.equal(identity.origin.profile.email, null);
  assert.equal(identity.origin.profile.verified, false);
  assert.deepEqual(identity.origin.profile.unknown.nested, [
    null,
    false,
    { text: "original" },
  ]);
  assert.ok(identity.rawJSON.includes(fixture.profileJSON));
  assert.ok(identity.rawJSON.includes('"id":9007199254740993'));
  assert.deepEqual(JSON.parse(identity.rawJSON), {
    provider: "google",
    id: "integration-user",
    email: null,
    email_verified: false,
    name: "Integration User",
    avatar_url: null,
    origin: identity.origin,
  });
};

let code = await login();
await assert.rejects(
  clientFor({ ...fixture.app, secret: "incorrect-secret" }).retrieve(code),
  apiError(401, "invalid_app"),
);
await assert.rejects(
  clientFor(fixture.other).retrieve(code),
  apiError(410, "result_unavailable"),
);
identityMatches(await client.retrieve(code));
await assert.rejects(
  client.retrieve(code),
  apiError(410, "result_unavailable"),
);

code = await login();
const claims = await Promise.allSettled(
  Array.from({ length: 12 }, () => client.retrieve(code)),
);
const winners = claims.filter(({ status }) => status === "fulfilled");
assert.equal(winners.length, 1);
identityMatches(winners[0].value);
for (const claim of claims) {
  if (claim.status === "rejected") {
    apiError(410, "result_unavailable")(claim.reason);
  }
}
console.log(
  "JavaScript SDK: real gateway handover, App isolation, exact JSON, and one-time concurrency passed.",
);
