import { test, after } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "../harness/client.ts";
import { uniqueSlug } from "../harness/fixtures.ts";
import { writeReport } from "../harness/report.ts";

// Black-box conformance for the templates API (beta/unstable per api/openapi.yaml,
// tag: templates). Covers all 8 ops: listStarterTemplates, getStarterTemplate,
// listTemplates, createTemplate, validateTemplate, deleteTemplate, getTemplate,
// updateTemplate (PATCH). Shapes/status pinned against openapi.yaml and verified
// by curl-probing the live staging server. Every created template is deleted
// inline in a finally. Cleanup is self-contained (not the shared cleanup harness,
// which only tracks agents/domains). The conformance account is internal-class
// (rate-limit-exempt, high-cap) so churn is free.
const SUITE = "17-templates";
const client = new ApiClient();

interface StarterTemplateVariable {
  name: string;
  required: boolean;
  raw: boolean;
  description: string;
  example: string;
}
interface StarterTemplateView {
  alias: string;
  name: string;
  description: string;
  version: string;
  subject: string;
  variables: StarterTemplateVariable[];
}
interface StarterTemplateDetailView extends StarterTemplateView {
  text: string;
  html: string;
}
interface Page<T> {
  items: T[];
  next_cursor: string | null;
}
interface TemplateSummaryView {
  id: string;
  name: string;
  subject: string;
  alias?: string;
  created_at: string;
  updated_at: string;
}
interface TemplateView extends TemplateSummaryView {
  text: string;
  html?: string;
  from_starter_alias?: string;
  from_starter_version?: string;
}
interface ValidateTemplateResponse {
  valid: boolean;
  errors: Array<{ part: string; message: string }>;
  rendered?: { subject: string; text: string; html?: string };
  suggested_data?: Record<string, unknown>;
}
interface ErrEnv {
  error?: { code?: string; message?: string; request_id?: string };
}

function delTemplate(id: string) {
  // deleteTemplate now requires ?confirm=DELETE (uniform destructive-delete
  // guard, #53) — a plain DELETE is rejected with 422 before the handler runs.
  return client.delete(`/v1/templates/${encodeURIComponent(id)}?confirm=DELETE`);
}

// ---------- starter templates ----------

test("listStarterTemplates: 200 with {items, next_cursor} envelope", async () => {
  const r = await client.get<Page<StarterTemplateView>>("/v1/starter-templates");
  assert.equal(r.status, 200, `expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.ok(Array.isArray(r.body?.items), "items is an array");
  assert.ok("next_cursor" in (r.body as object), "next_cursor key present (required)");
  assert.ok(
    r.body!.next_cursor === null || typeof r.body!.next_cursor === "string",
    "next_cursor is string|null",
  );
  assert.ok(r.body!.items.length >= 1, "deployment ships at least one starter template");
  for (const s of r.body!.items) {
    assert.ok(s.alias, "starter has alias");
    assert.ok(s.name, "starter has name");
    assert.equal(typeof s.description, "string", "starter has description");
    assert.ok(s.version, "starter has version");
    assert.equal(typeof s.subject, "string", "starter has subject source");
    assert.ok(Array.isArray(s.variables), "starter has variables[]");
  }
});

test("getStarterTemplate: 200 detail view (body + html) for an alias from the list", async () => {
  const list = await client.get<Page<StarterTemplateView>>("/v1/starter-templates");
  assert.equal(list.status, 200);
  const alias = list.body!.items[0]?.alias;
  assert.ok(alias, "picked a starter alias from the list");

  const r = await client.get<StarterTemplateDetailView>(
    `/v1/starter-templates/${encodeURIComponent(alias)}`,
  );
  assert.equal(r.status, 200, `expected 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.equal(r.body?.alias, alias, "detail alias matches request");
  // The detail view adds the full body sources over the list (summary) view.
  assert.equal(typeof r.body?.text, "string", "detail carries plain-text body source");
  assert.equal(typeof r.body?.html, "string", "detail carries html source");
  assert.equal(typeof r.body?.subject, "string", "detail carries subject source");
  assert.ok(Array.isArray(r.body?.variables), "detail carries variables[]");
  for (const v of r.body!.variables) {
    assert.ok(v.name, "variable has name");
    assert.equal(typeof v.required, "boolean", "variable.required is boolean");
    assert.equal(typeof v.raw, "boolean", "variable.raw is boolean");
  }
});

test("getStarterTemplate: nonexistent alias returns 404", async () => {
  const r = await client.get<ErrEnv>(`/v1/starter-templates/nope-${uniqueSlug("s")}`);
  assert.equal(r.status, 404, `nonexistent starter → 404, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.equal(r.body?.error?.code, "starter_template_not_found");
});

// ---------- user templates: lifecycle ----------

test("createTemplate + getTemplate + listTemplates + deleteTemplate lifecycle", async () => {
  const alias = uniqueSlug("tmpl");
  let id: string | null = null;
  try {
    const create = await client.post<TemplateView>("/v1/templates", {
      body: {
        name: `e2e ${alias}`,
        subject: "Hi {{name}}",
        text: "Hello {{name}}, welcome to {{company}}.",
        alias,
      },
    });
    assert.equal(create.status, 201, `create → 201, got ${create.status}: ${create.raw.slice(0, 200)}`);
    id = create.body!.id;
    assert.ok(id?.startsWith("tmpl_"), `id has tmpl_ prefix: ${id}`);
    assert.equal(create.body?.name, `e2e ${alias}`);
    assert.equal(create.body?.subject, "Hi {{name}}");
    assert.equal(create.body?.text, "Hello {{name}}, welcome to {{company}}.");
    assert.equal(create.body?.alias, alias, "alias echoed back");
    assert.ok(create.body?.created_at, "created_at present");
    assert.ok(create.body?.updated_at, "updated_at present");

    const got = await client.get<TemplateView>(`/v1/templates/${id}`);
    assert.equal(got.status, 200, `getTemplate → 200, got ${got.status}`);
    assert.equal(got.body?.id, id);
    assert.equal(got.body?.text, "Hello {{name}}, welcome to {{company}}.", "get returns full body source");

    const list = await client.get<Page<TemplateSummaryView>>("/v1/templates");
    assert.equal(list.status, 200);
    assert.ok("next_cursor" in (list.body as object), "next_cursor present in page");
    assert.ok(
      list.body!.items.some((t) => t.id === id),
      "created template appears in listTemplates",
    );

    const del = await delTemplate(id);
    assert.equal(del.status, 200, `deleteTemplate → 200 + deletion object, got ${del.status}: ${del.raw.slice(0, 200)}`);
    const delBody = JSON.parse(del.raw) as { deleted?: boolean; id?: string };
    assert.equal(delBody.deleted, true, "deletion object has deleted:true");
    assert.equal(delBody.id, id, "deletion object echoes the template id");
    id = null;

    const after = await client.get<ErrEnv>(`/v1/templates/${create.body!.id}`);
    assert.equal(after.status, 404, `deleted template → 404, got ${after.status}`);
    assert.equal(after.body?.error?.code, "not_found");
  } finally {
    if (id) await delTemplate(id);
  }
});

test("updateTemplate (PATCH): partial update mutates name and bumps updated_at", async () => {
  const alias = uniqueSlug("tmpl");
  let id: string | null = null;
  try {
    const create = await client.post<TemplateView>("/v1/templates", {
      body: { name: "before", subject: "S {{x}}", text: "B {{x}}", alias },
    });
    assert.equal(create.status, 201, `create → 201, got ${create.status}: ${create.raw.slice(0, 200)}`);
    id = create.body!.id;

    const patch = await client.patch<TemplateView>(`/v1/templates/${id}`, {
      body: { name: "after", subject: "S2 {{x}}" },
    });
    assert.equal(patch.status, 200, `PATCH → 200, got ${patch.status}: ${patch.raw.slice(0, 200)}`);
    assert.equal(patch.body?.name, "after", "name updated");
    assert.equal(patch.body?.subject, "S2 {{x}}", "subject updated");
    assert.equal(patch.body?.text, "B {{x}}", "unchanged part preserved");
    assert.equal(patch.body?.alias, alias, "alias preserved when not in patch");

    const got = await client.get<TemplateView>(`/v1/templates/${id}`);
    assert.equal(got.body?.name, "after", "PATCH persisted");
  } finally {
    if (id) await delTemplate(id);
  }
});

test("createTemplate: from_starter copies a starter verbatim (201, carries provenance fields)", async () => {
  const list = await client.get<Page<StarterTemplateView>>("/v1/starter-templates");
  const starterAlias = list.body!.items[0]?.alias;
  assert.ok(starterAlias, "have a starter alias to copy");
  const alias = uniqueSlug("fromst");
  let id: string | null = null;
  try {
    const create = await client.post<TemplateView>("/v1/templates", {
      body: { from_starter: starterAlias, alias },
    });
    assert.equal(create.status, 201, `from_starter create → 201, got ${create.status}: ${create.raw.slice(0, 200)}`);
    id = create.body!.id;
    assert.ok(id?.startsWith("tmpl_"));
    assert.equal(create.body?.from_starter_alias, starterAlias, "from_starter_alias records provenance");
    assert.ok(create.body?.from_starter_version, "from_starter_version records catalog version at copy time");
    assert.ok(create.body?.text, "copied body source is populated");
  } finally {
    if (id) await delTemplate(id);
  }
});

// ---------- validateTemplate ----------

test("validateTemplate: valid source → 200 valid:true with rendered preview + suggested_data", async () => {
  const r = await client.post<ValidateTemplateResponse>("/v1/templates/validate", {
    body: {
      subject: "Hi {{name}}",
      text: "Welcome {{name}} to {{company}}",
      test_data: { name: "Ada", company: "Acme" },
    },
  });
  assert.equal(r.status, 200, `validate → 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.equal(r.body?.valid, true, "valid source reports valid:true");
  assert.deepEqual(r.body?.errors, [], "no errors for valid source");
  assert.equal(r.body?.rendered?.subject, "Hi Ada", "subject rendered against test_data");
  assert.equal(r.body?.rendered?.text, "Welcome Ada to Acme", "body rendered against test_data");
  assert.ok(r.body?.suggested_data, "suggested_data present (placeholder per referenced variable)");
  assert.ok(
    "name" in (r.body!.suggested_data as object) && "company" in (r.body!.suggested_data as object),
    "suggested_data covers every referenced variable",
  );
});

test("validateTemplate: invalid source → HTTP 200 with valid:false + per-part errors", async () => {
  // Pinned behavior: a parse error is a 200 payload (valid:false + errors[]),
  // NOT a 4xx. openapi documents only 200/ValidateTemplateResponse for this op
  // and ValidateTemplateResponse.valid is a boolean, so the parse verdict rides
  // in the body. (Verified against live server.)
  const r = await client.post<ValidateTemplateResponse>("/v1/templates/validate", {
    body: { subject: "Hi {{name", text: "ok" },
  });
  assert.equal(r.status, 200, `invalid template still returns 200, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.equal(r.body?.valid, false, "unparseable source reports valid:false");
  assert.ok(Array.isArray(r.body?.errors) && r.body!.errors.length >= 1, "errors[] is populated");
  const subjErr = r.body!.errors.find((e) => e.part === "subject");
  assert.ok(subjErr, "error is attributed to the offending part (subject)");
  assert.ok(subjErr!.message, "error carries a message");
});

// ---------- negatives ----------

test("templates: unauthenticated list returns 401", async () => {
  const r = await client.get("/v1/templates", { apiKey: null });
  assert.equal(r.status, 401, `unauth → 401, got ${r.status}: ${r.raw.slice(0, 200)}`);
});

test("getTemplate: nonexistent id returns 404", async () => {
  const r = await client.get<ErrEnv>(`/v1/templates/tmpl_${"0".repeat(32)}`);
  assert.equal(r.status, 404, `nonexistent get → 404, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.equal(r.body?.error?.code, "not_found");
});

test("deleteTemplate: nonexistent id returns 404", async () => {
  const r = await client.delete<ErrEnv>(`/v1/templates/tmpl_${"0".repeat(32)}?confirm=DELETE`);
  assert.equal(r.status, 404, `nonexistent delete → 404, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.equal(r.body?.error?.code, "not_found");
});

test("createTemplate: missing required fields returns 4xx", async () => {
  // CreateTemplateRequest requires name + subject + body (unless from_starter).
  const r = await client.post<ErrEnv>("/v1/templates", { body: {} });
  assert.ok(r.status >= 400 && r.status < 500, `bad create → 4xx, got ${r.status}: ${r.raw.slice(0, 200)}`);
  assert.equal(r.status, 400, `documented invalid_request → 400, got ${r.status}`);
  assert.equal(r.body?.error?.code, "invalid_request");
});

test("createTemplate: duplicate alias returns 409 alias_taken", async () => {
  const alias = uniqueSlug("dup");
  let id: string | null = null;
  try {
    const first = await client.post<TemplateView>("/v1/templates", {
      body: { name: "first", subject: "S {{x}}", text: "B {{x}}", alias },
    });
    assert.equal(first.status, 201, `first create → 201, got ${first.status}: ${first.raw.slice(0, 200)}`);
    id = first.body!.id;

    const second = await client.post<ErrEnv>("/v1/templates", {
      body: { name: "second", subject: "S {{x}}", text: "B {{x}}", alias },
    });
    assert.equal(second.status, 409, `dup alias → 409, got ${second.status}: ${second.raw.slice(0, 200)}`);
    assert.equal(second.body?.error?.code, "alias_taken");
  } finally {
    if (id) await delTemplate(id);
  }
});

after(async () => {
  await writeReport(`./reports/${SUITE}.json`);
});
