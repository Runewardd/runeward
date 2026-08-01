import assert from "node:assert/strict";
import test from "node:test";

import {
  RunewardApprovalRequired,
  RunewardClient,
  RunewardDenied,
} from "../dist/index.js";

test("the SDK preserves existing concepts and governance verdicts", async () => {
  assert.throws(
    () => new RunewardClient({ baseUrl: "http://control.example.com" }),
    /refusing insecure http/,
  );

  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (url, init) => {
    requests.push({ url, init });
    return new Response(JSON.stringify({ id: "cit_123", profile: "dev" }), {
      status: 201,
      headers: { "content-type": "application/json" },
    });
  };

  try {
    const created = await new RunewardClient().createSandbox("dev");
    assert.equal(created.id, "cit_123");
    assert.equal(requests[0].url, "http://localhost:8080/v1/citadels");
    assert.deepEqual(JSON.parse(requests[0].init.body), { profile: "dev" });
  } finally {
    globalThis.fetch = originalFetch;
  }

  assert.equal(new RunewardDenied("blocked").status, 403);
  assert.equal(new RunewardApprovalRequired("apr_123").status, 202);
});
