import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  createClient,
  generateState,
  parseCallback,
} from "../../sdk/js/dist/index.js";

const fixture = JSON.parse(readFileSync(0, "utf8"));
const client = createClient({
  baseURL: fixture.baseURL,
  appID: fixture.app.app.id,
  appSecret: fixture.app.secret,
  allowLoopbackHTTP: true,
  timeoutMs: 5_000,
});
const browserFetch = (url, options = {}) =>
  fetch(url, {
    ...options,
    redirect: "manual",
    signal: AbortSignal.timeout(5_000),
  });

for (const tenant of fixture.tenants) {
  const state = generateState();
  const started = await browserFetch(
    client.authorizationURL({
      provider: "microsoft",
      redirectURI: fixture.redirectURI,
      state,
    }),
  );
  await started.arrayBuffer();
  assert.equal(started.status, 302);
  const upstream = new URL(started.headers.get("Location"));
  const cookies = started.headers.getSetCookie();
  assert.equal(cookies.length, 1);
  assert.match(cookies[0], /HttpOnly/);
  assert.notEqual(upstream.searchParams.get("state"), state);
  const callback = new URL("/auth/callback/microsoft", fixture.baseURL);
  callback.searchParams.set("state", upstream.searchParams.get("state"));
  callback.searchParams.set(
    "code",
    `${tenant}:${upstream.searchParams.get("nonce")}`,
  );
  const completed = await browserFetch(callback, {
    headers: { Cookie: cookies[0].split(";", 1)[0] },
  });
  await completed.arrayBuffer();
  assert.equal(completed.status, 302);
  const product = new URL(completed.headers.get("Location"));
  assert.equal(product.searchParams.get("existing"), "preserved");
  const identity = await client.retrieve(
    parseCallback(product.searchParams, state),
  );
  assert.equal(identity.provider, "microsoft");
  assert.equal(identity.id, `msft:${fixture.objectID}@${tenant}`);
  assert.equal(identity.email, null);
  assert.equal(identity.email_verified, null);
  assert.equal(identity.name, null);
  assert.equal(identity.avatar_url, null);
  assert.equal(identity.origin.id_token_claims.tid, tenant);
  assert.deepEqual(identity.origin.id_token_claims.unknown, {
    nested: [null, false],
  });
  assert.equal(Object.keys(JSON.parse(identity.rawJSON)).length, 7);
}
console.log(
  "JavaScript SDK: Microsoft personal and organizational identities passed through the gateway.",
);
