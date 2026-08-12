import assert from "node:assert/strict";
import test from "node:test";

import {
  RunewardApprovalRequired,
	RunewardAuthorizationError,
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
  assert.equal(new RunewardAuthorizationError("wrong owner").status, 403);
});

test("run lineage and Cohort completion use the v1 endpoints", async () => {
  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (url, init) => {
    requests.push({ url, init });
    const payload = url.endsWith("/v1/runs") ? { runs: [{ id: "run-1" }] } : { ok: true };
    return new Response(JSON.stringify(payload), { status: 200 });
  };

  try {
    const client = new RunewardClient();
    assert.equal((await client.listRuns())[0].id, "run-1");
    await client.completeTask("cohort/1", "task/1", "signed-lease", "done");
    assert.equal(
      requests[1].url,
      "http://localhost:8080/v1/cohorts/cohort%2F1/tasks/task%2F1/complete",
    );
    assert.deepEqual(JSON.parse(requests[1].init.body), {
      owner: "",
      lease_token: "signed-lease",
      result: "done",
    });
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("conversation turns publish to the Citadel live feed", async () => {
  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.fetch = async (url, init) => {
    requests.push({ url, init });
    return new Response(JSON.stringify({ id: 1, role: "assistant" }), { status: 201 });
  };
  try {
    const client = new RunewardClient();
    await client.publishConversation("cit/1", "assistant", "Working on it", "run-1");
    assert.equal(requests[0].url, "http://localhost:8080/v1/citadels/cit%2F1/conversation");
    assert.deepEqual(JSON.parse(requests[0].init.body), {
      role: "assistant", content: "Working on it", run_id: "run-1",
    });
  } finally {
    globalThis.fetch = originalFetch;
  }
});
