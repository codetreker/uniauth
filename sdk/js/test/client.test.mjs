import assert from "node:assert/strict";
import { once } from "node:events";
import { createServer } from "node:http";
import test from "node:test";
import {
  APIError,
  CallbackError,
  ProtocolError,
  createClient,
  generateState,
  parseCallback,
} from "../dist/index.js";

const config = {
  baseURL: "https://auth.example",
  appID: "product",
  appSecret: "secret",
};
const identityJSON =
  '{ "provider":"github", "id":"github:123", "email":"Chosen@Example.COM", "email_verified":false, "name":null, "avatar_url":"https://images.example/avatar", "origin":{"profile":{"id":9007199254740993,"email":null,"enabled":false,"nested":{"new":[1,null,false]}},"emails":[],"future":null},"future_envelope":true }\n';
const jsonResponse = (body = identityJSON, status = 200) =>
  new Response(body, {
    status,
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "X-Request-ID": "request-123",
    },
  });
const callbackError = (code) => (error) =>
  error instanceof CallbackError && error.code === code;

test("authorization preserves callback spelling, Unicode, and query encoding", () => {
  const callback =
    "https://PRODUCT.example:443/%61uth/callback?tag=a%2fb&repeat=1&repeat=2&label=%E6%B5%8B%E8%AF%95";
  const state = "state + / & 中文 😀";
  const client = createClient({ ...config, appID: "产品" });
  const url = new URL(
    client.authorizationURL({
      provider: "github",
      redirectURI: callback,
      state,
    }),
  );
  assert.equal(url.origin, config.baseURL);
  assert.equal(url.pathname, "/auth/authorize");
  assert.deepEqual(
    [...url.searchParams],
    [
      ["app_id", "产品"],
      ["provider", "github"],
      ["redirect_uri", callback],
      ["state", state],
    ],
  );
});

test("base origins, loopback opt-in, and registered callback rules are enforced", () => {
  for (const baseURL of [
    "http://auth.example",
    "https://user:password@auth.example",
    "https://@auth.example",
    "https://auth.example/path",
    "https://auth.example/a/../",
    "https://auth.example?",
    "https://auth.example#",
    " https://auth.example",
    "https:\\auth.example",
    "http://127.1",
    "http://2130706433",
    "http://[::ffff:128.0.0.1]",
    "http://[::ffff:126.0.0.1]",
    "http://[::2]",
    "http://[::127.0.0.1]",
  ]) {
    assert.throws(
      () => createClient({ ...config, baseURL, allowLoopbackHTTP: true }),
      TypeError,
    );
  }
  for (const baseURL of [
    "http://localhost:8080",
    "http://127.0.0.1:8080",
    "http://127.10.20.30:8080",
    "http://[::1]:8080",
    "http://[0:0:0:0:0:0:0:1]:8080",
    "http://[::ffff:127.0.0.1]:8080",
    "http://[::ffff:7f00:1]:8080",
  ]) {
    assert.throws(() => createClient({ ...config, baseURL }), TypeError);
    assert.doesNotThrow(() =>
      createClient({ ...config, baseURL, allowLoopbackHTTP: true }),
    );
  }
  const client = createClient(config);
  for (const redirectURI of [
    "http://product.example/callback",
    "https://user@product.example/callback",
    "https://product.example/callback#",
    "https://product.example/callback?%63ode=reserved",
    "https://product.example/callback?state=reserved",
    "https://product.example/callback?error=reserved",
    "https://product.example/callback?key=%xy",
    "https://product.example/callback?key=1;other=2",
  ]) {
    assert.throws(
      () =>
        client.authorizationURL({
          provider: "google",
          redirectURI,
          state: "s",
        }),
      TypeError,
    );
  }
});

test("UTF-8 byte limits and lone surrogate rejection precede transport", async () => {
  assert.doesNotThrow(() => createClient({ ...config, appID: "é".repeat(64) }));
  for (const overrides of [
    { appID: "é".repeat(65) },
    { appID: "app:id" },
    { appID: "\ud800" },
    { appSecret: "\udfff" },
    { appSecret: "é".repeat(257) },
    { timeoutMs: 0 },
    { timeoutMs: -1 },
    { timeoutMs: 1.5 },
    { timeoutMs: NaN },
    { timeoutMs: 2_147_483_648 },
    { fetch: null },
    { allowLoopbackHTTP: "true" },
  ]) {
    assert.throws(() => createClient({ ...config, ...overrides }), TypeError);
  }
  assert.doesNotThrow(() =>
    createClient({ ...config, timeoutMs: 2_147_483_647 }),
  );
  let calls = 0;
  const client = createClient({
    ...config,
    fetch: async () => {
      calls++;
      return jsonResponse();
    },
  });
  for (const code of ["", "é".repeat(257), "\ud800"]) {
    await assert.rejects(client.retrieve(code), TypeError);
  }
  assert.equal(calls, 0);
  await client.retrieve("é".repeat(256));
  assert.equal(calls, 1);
  for (const state of ["", "é".repeat(1025), "\ud800"]) {
    assert.throws(
      () =>
        client.authorizationURL({
          provider: "microsoft",
          redirectURI: "https://product.example/callback",
          state,
        }),
      TypeError,
    );
  }
});

test("generated state has 256 random bits in base64url", () => {
  const states = Array.from({ length: 128 }, generateState);
  assert.equal(new Set(states).size, states.length);
  for (const state of states) {
    assert.match(state, /^[A-Za-z0-9_-]{43}$/);
    assert.equal(Buffer.from(state, "base64url").length, 32);
  }
});

test("callback parsing validates state before code and provider failures", () => {
  assert.equal(
    parseCallback(
      new URLSearchParams("state=s%2B%20%F0%9F%98%80&code=c%2B"),
      "s+ 😀",
    ),
    "c+",
  );
  for (const query of [
    "code=c",
    "state=&code=c",
    "state=s&state=s&code=c",
    `state=${"é".repeat(1025)}&code=c`,
  ]) {
    assert.throws(
      () => parseCallback(new URLSearchParams(query), "s"),
      callbackError("invalid_callback"),
    );
  }
  for (const query of [
    "state=other&error=provider_denied",
    "state=other&code=c&code=c",
    "state=other&error=unknown",
    "state=other&code=c&error=provider_failed",
  ]) {
    assert.throws(
      () => parseCallback(new URLSearchParams(query), "s"),
      callbackError("state_mismatch"),
    );
  }
  for (const suffix of [
    "",
    "code=",
    "error=",
    "code=c&code=c",
    "error=provider_denied&error=provider_denied",
    "code=c&error=provider_denied",
    "code=c&error=",
    "code=&error=provider_denied",
    "error=unknown",
    `code=${"é".repeat(257)}`,
    `error=${"é".repeat(129)}`,
  ]) {
    assert.throws(
      () => parseCallback(new URLSearchParams(`state=s&${suffix}`), "s"),
      callbackError("invalid_callback"),
    );
  }
  for (const code of [
    "provider_denied",
    "provider_failed",
    "capacity_exceeded",
  ]) {
    assert.throws(
      () =>
        parseCallback(new URLSearchParams({ state: "s", error: code }), "s"),
      callbackError(code),
    );
  }
  assert.throws(
    () =>
      parseCallback(new URLSearchParams({ state: "s", code: "c" }), "\ud800"),
    callbackError("invalid_callback"),
  );
});

test("retrieval sends exact Basic JSON request and preserves the full raw identity", async () => {
  let calls = 0;
  const client = createClient({
    ...config,
    appID: "产品",
    appSecret: "秘密:colon",
    fetch: async (url, options) => {
      calls++;
      assert.equal(url, "https://auth.example/auth/result");
      assert.equal(options.method, "POST");
      assert.equal(options.redirect, "manual");
      assert.equal(
        options.headers.Authorization,
        `Basic ${Buffer.from("产品:秘密:colon").toString("base64")}`,
      );
      assert.equal(options.headers["Content-Type"], "application/json");
      assert.equal(options.headers.Accept, "application/json");
      assert.equal(options.body, '{"code":"once"}');
      assert.ok(options.signal instanceof AbortSignal);
      assert.equal(Object.keys(options.headers).length, 3);
      return jsonResponse();
    },
  });
  const identity = await client.retrieve("once");
  assert.equal(calls, 1);
  assert.equal(identity.rawJSON, identityJSON);
  assert.equal(identity.provider, "github");
  assert.equal(identity.id, "github:123");
  assert.equal(identity.email, "Chosen@Example.COM");
  assert.equal(identity.email_verified, false);
  assert.equal(identity.name, null);
  assert.equal(identity.avatar_url, "https://images.example/avatar");
  assert.deepEqual(identity.origin, {
    profile: {
      id: 9007199254740992,
      email: null,
      enabled: false,
      nested: { new: [1, null, false] },
    },
    emails: [],
    future: null,
  });
  assert.match(identity.rawJSON, /9007199254740993/);
  assert.match(identity.rawJSON, /future_envelope/);
});

test("all nullable fields, verification values, and wire strings are preserved", async () => {
  for (const provider of ["microsoft", "google", "github"]) {
    for (const email_verified of [null, false, true]) {
      const payload = {
        provider,
        id: "opaque-wire-id",
        email: null,
        email_verified,
        name: null,
        avatar_url: null,
        origin: { optional: null, enabled: false, unknown: [null, false, {}] },
      };
      const rawJSON = JSON.stringify(payload);
      const client = createClient({
        ...config,
        fetch: async () => jsonResponse(rawJSON),
      });
      assert.deepEqual(await client.retrieve("code"), { ...payload, rawJSON });
    }
  }
  const payload = {
    provider: "google",
    id: "server-selected-id",
    email: "  Mixed@Example.COM  ",
    email_verified: false,
    name: "",
    avatar_url: "verbatim-image-value",
    origin: {
      userinfo: { sub: "different-origin-id", name: "Ignored source name" },
    },
  };
  const rawJSON = JSON.stringify(payload);
  const client = createClient({
    ...config,
    fetch: async () => jsonResponse(rawJSON),
  });
  assert.deepEqual(await client.retrieve("code"), { ...payload, rawJSON });
});

test("every identity field is required and invalid field types fail validation", async () => {
  const baseline = JSON.parse(identityJSON);
  const invalid = {
    provider: [null, false, 1, {}, [], "unknown"],
    id: [null, false, 1, {}, [], "", "\ud800"],
    email: [false, 1, {}, [], "\ud800"],
    email_verified: ["false", 0, {}, []],
    name: [false, 1, {}, [], "\ud800"],
    avatar_url: [false, 1, {}, [], "\ud800"],
    origin: [null, false, 1, "data", []],
  };
  for (const [field, values] of Object.entries(invalid)) {
    const missing = { ...baseline };
    delete missing[field];
    for (const payload of [
      missing,
      ...values.map((value) => ({ ...baseline, [field]: value })),
    ]) {
      const client = createClient({
        ...config,
        fetch: async () => jsonResponse(JSON.stringify(payload)),
      });
      await assert.rejects(client.retrieve("code"), ProtocolError);
    }
  }
});

test("the obsolete subject and sources envelope is rejected", async () => {
  const client = createClient({
    ...config,
    fetch: async () =>
      jsonResponse(
        '{"provider":"github","subject":"github:123","sources":{"profile":{"id":123}}}',
      ),
  });
  await assert.rejects(client.retrieve("code"), ProtocolError);
});

test("identity responses larger than 8 MiB are supported", async () => {
  const photo = "a".repeat(9 * 1024 * 1024);
  const raw = JSON.stringify({
    provider: "microsoft",
    id: "microsoft:tenant:oid",
    email: null,
    email_verified: null,
    name: null,
    avatar_url: `data:image/jpeg;base64,${photo}`,
    origin: { photo: { content_type: "image/jpeg", data_base64: photo } },
  });
  const client = createClient({
    ...config,
    fetch: async () => jsonResponse(raw),
  });
  const identity = await client.retrieve("code");
  assert.equal(identity.rawJSON, raw);
  assert.equal(identity.origin.photo.data_base64.length, photo.length);
  assert.equal(identity.avatar_url, `data:image/jpeg;base64,${photo}`);
});

test("structured API errors preserve status, future codes, message, and request ID", async () => {
  for (const status of [400, 401, 410, 418, 500, 503]) {
    const client = createClient({
      ...config,
      fetch: async () =>
        jsonResponse(
          '{"error":{"code":"future_code","message":"A safe service message."},"extra":false}',
          status,
        ),
    });
    await assert.rejects(client.retrieve("code"), (error) => {
      assert.ok(error instanceof APIError);
      assert.equal(error.status, status);
      assert.equal(error.code, "future_code");
      assert.equal(error.message, "A safe service message.");
      assert.equal(error.requestID, "request-123");
      return true;
    });
  }
});

test("protocol errors omit private request and response values", async () => {
  const secret = "secret-do-not-log";
  const code = "code-do-not-log";
  const cases = [
    () => jsonResponse(`not json ${secret} ${code}`),
    () => jsonResponse(`\ufeff${identityJSON}`),
    () =>
      new Response("private html", {
        headers: { "Content-Type": "text/html" },
      }),
    () => jsonResponse(identityJSON, 201),
    () =>
      new Response(secret, {
        status: 302,
        headers: { Location: "https://other.example" },
      }),
    () =>
      jsonResponse(
        '{"provider":"github","id":"id","email":null,"email_verified":false,"name":null,"avatar_url":null,"origin":[]}',
      ),
    () => jsonResponse('{"error":{"code":"invalid_app"}}', 401),
    () => jsonResponse('{"error":{"code":"","message":"x"}}', 400),
    () => jsonResponse('{"error":{"code":"x","message":false}}', 500),
    () =>
      new Response(new Uint8Array([0xff]), {
        headers: { "Content-Type": "application/json" },
      }),
  ];
  for (const response of cases) {
    const client = createClient({
      ...config,
      appSecret: secret,
      fetch: async () => response(),
    });
    await assert.rejects(client.retrieve(code), (error) => {
      assert.ok(error instanceof ProtocolError);
      assert.ok(!error.message.includes(secret));
      assert.ok(!error.message.includes(code));
      assert.equal(error.cause, undefined);
      return true;
    });
  }
});

test("network, abort, and body-stream failures preserve the original error object", async () => {
  const cause = new Error("network cause");
  const networkError = new TypeError("fetch failed", { cause });
  let calls = 0;
  const client = createClient({
    ...config,
    fetch: async () => {
      calls++;
      throw networkError;
    },
  });
  await assert.rejects(
    client.retrieve("code"),
    (error) => error === networkError,
  );
  assert.equal(calls, 1);
  assert.equal(networkError.cause, cause);

  const controller = new AbortController();
  const abort = new Error("caller canceled");
  const abortClient = createClient({
    ...config,
    fetch: async (_url, { signal }) => {
      controller.abort(abort);
      throw signal.reason;
    },
  });
  await assert.rejects(
    abortClient.retrieve("code", { signal: controller.signal }),
    (error) => error === abort,
  );

  const streamError = new Error("body failed");
  const streamClient = createClient({
    ...config,
    fetch: async () =>
      new Response(
        new ReadableStream({ start: (stream) => stream.error(streamError) }),
        {
          headers: { "Content-Type": "application/json" },
        },
      ),
  });
  await assert.rejects(
    streamClient.retrieve("code"),
    (error) => error === streamError,
  );
});

test("the configured timeout and caller signal both cancel the fetch", async () => {
  const keepAlive = setTimeout(() => {}, 1000);
  try {
    const client = createClient({
      ...config,
      timeoutMs: 10,
      fetch: async (_url, { signal }) =>
        new Promise((resolve, reject) => {
          signal.addEventListener("abort", () => reject(signal.reason), {
            once: true,
          });
        }),
    });
    const controller = new AbortController();
    await assert.rejects(
      client.retrieve("code", { signal: controller.signal }),
      {
        name: "TimeoutError",
      },
    );
    assert.equal(controller.signal.aborted, false);
  } finally {
    clearTimeout(keepAlive);
  }
});

const withServer = async (handler, run) => {
  const server = createServer(handler);
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  try {
    await run(`http://127.0.0.1:${address.port}`);
  } finally {
    server.closeAllConnections();
    await new Promise((resolve, reject) =>
      server.close((error) => (error ? reject(error) : resolve())),
    );
  }
};

test("native fetch never follows result redirects or retries failed retrieval", async () => {
  let resultRequests = 0;
  let targetRequests = 0;
  await withServer(
    (request, response) => {
      if (request.url === "/target") {
        targetRequests++;
        response.end(identityJSON);
      } else {
        resultRequests++;
        request.resume();
        response.writeHead(307, { Location: "/target" });
        response.end();
      }
    },
    async (baseURL) => {
      const client = createClient({
        ...config,
        baseURL,
        allowLoopbackHTTP: true,
      });
      await assert.rejects(client.retrieve("code"), ProtocolError);
    },
  );
  assert.equal(resultRequests, 1);
  assert.equal(targetRequests, 0);
});

test("concurrent retrieval exposes one-time consumption without local serialization", async () => {
  let consumed = false;
  let requests = 0;
  await withServer(
    async (request, response) => {
      requests++;
      let body = "";
      for await (const chunk of request) body += chunk;
      assert.equal(request.method, "POST");
      assert.equal(
        request.headers.authorization,
        `Basic ${Buffer.from("product:secret").toString("base64")}`,
      );
      assert.equal(body, '{"code":"once"}');
      assert.equal(request.headers["idempotency-key"], undefined);
      response.setHeader("Content-Type", "application/json");
      if (consumed) {
        response.writeHead(410);
        response.end(
          '{"error":{"code":"result_unavailable","message":"Unavailable."}}',
        );
      } else {
        consumed = true;
        response.end(identityJSON);
      }
    },
    async (baseURL) => {
      const client = createClient({
        ...config,
        baseURL,
        allowLoopbackHTTP: true,
      });
      const results = await Promise.allSettled([
        client.retrieve("once"),
        client.retrieve("once"),
      ]);
      assert.equal(
        results.filter((result) => result.status === "fulfilled").length,
        1,
      );
      const rejected = results.find((result) => result.status === "rejected");
      assert.ok(rejected.reason instanceof APIError);
      assert.equal(rejected.reason.code, "result_unavailable");
    },
  );
  assert.equal(requests, 2);
});
