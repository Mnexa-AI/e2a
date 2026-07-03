import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpClient } from "../client.js";
import { z } from "zod";
import { runTool, strictInputSchema } from "./util.js";

/**
 * Template tools (beta) — reusable email templates + the read-only starter
 * catalog, and the send-side reference lives on `send_message`
 * (template_id / template_alias / template_data).
 *
 * All eight tools are account-scope (the backend 403s agent-scoped
 * credentials), so they sit in ADMIN_TOOLS. Handlers ride the SDK's
 * `templates` resource via the shared E2AClient, so results are camelCase
 * SDK views (htmlBody, createdAt, suggestedData, …) exactly like every
 * other tool; the snake_case tool ARG names below follow the house arg
 * style (html_body, from_starter) and are mapped to the SDK request
 * fields in each handler.
 *
 * The engine is a deliberately flat Mustache subset; the SYNTAX blurb below
 * is spliced into the tools where an LLM authors or debugs source.
 */

const BETA = "Beta: templates are unstable — their shape may change before they are declared stable.";

const SYNTAX =
  "Template syntax (flat Mustache subset): {{var}} interpolates (HTML-escaped in html_body only; raw in subject/body), " +
  "{{{var}}} interpolates raw everywhere; dot paths reach nested template_data ({{user.name}}). " +
  "NO loops/sections/partials/comments — {{#…}}, {{/…}}, {{^…}}, {{>…}}, {{!…}} are parse errors. " +
  "Missing variables render as EMPTY STRING, silently — typos don't error, they produce blank spots. " +
  "Lists/tables go through raw {{{…_html}}} fragment variables: you build the HTML fragment, and you MUST " +
  "HTML-escape any user-supplied content inside it yourself (raw slots bypass escaping — unescaped input is an injection hole).";

const APPROVAL_LINK_WARNING =
  "For the approval-request starter: approve_url/reject_url MUST point to CONFIRMATION PAGES that require an explicit " +
  "click (or auth) to act — NEVER state-changing GET endpoints. Email security scanners prefetch every link in a message, " +
  "so a bare GET-to-approve URL gets 'clicked' by a robot before any human sees the mail.";

export function registerTemplateTools(server: McpServer, client: McpClient): void {
  server.registerTool(
    "list_templates",
    {
      title: "List email templates (beta)",
      annotations: { readOnlyHint: true },
      description:
        "List the account's stored email templates, newest first — summary rows (id, name, alias, subject, timestamps); " +
        "`get_template` returns the full body sources. Use a template on send via `send_message`'s template_id or " +
        "template_alias. Read-only; cheap. " +
        BETA,
      inputSchema: strictInputSchema({}),
    },
    async () => runTool(async () => ({ templates: await client.listTemplates() })),
  );

  server.registerTool(
    "get_template",
    {
      title: "Get one email template (beta)",
      annotations: { readOnlyHint: true },
      description:
        "Fetch one stored template by id (tmpl_…), including its subject/body/htmlBody sources. Templates copied from a " +
        "starter also carry fromStarterAlias/fromStarterVersion (read-only provenance). " + BETA,
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("Template id (tmpl_…)."),
      }),
    },
    async (args) => runTool(() => client.getTemplate(args.id)),
  );

  server.registerTool(
    "create_template",
    {
      title: "Create an email template (beta)",
      annotations: { destructiveHint: false },
      description:
        "Create a reusable email template, in ONE of two mutually exclusive modes: " +
        "(1) literal source — name + subject + body required, alias/html_body optional; or " +
        "(2) from_starter — copy a starter template verbatim by alias (see `list_starter_templates`); name/alias default " +
        "to the starter's and may be overridden, but subject/body/html_body must NOT be passed (edit the created copy " +
        "afterwards with `update_template`). All parts must parse or the create is rejected. " +
        "Give the template an alias to reference it on send as template_alias without tracking ids. " +
        SYNTAX + " " + BETA,
      inputSchema: strictInputSchema({
        name: z
          .string()
          .optional()
          .describe("Human-readable template name. Required unless from_starter supplies the default."),
        alias: z
          .string()
          .optional()
          .describe(
            "Optional per-user unique handle ([A-Za-z][A-Za-z0-9._-]{0,127}) usable as template_alias on send_message.",
          ),
        subject: z
          .string()
          .optional()
          .describe("Subject template source ({{variable}} interpolation, never HTML-escaped). Required unless from_starter is set."),
        body: z
          .string()
          .optional()
          .describe("Plain-text body template source (never HTML-escaped). Required unless from_starter is set."),
        html_body: z
          .string()
          .optional()
          .describe("Optional HTML body template source ({{x}} is HTML-escaped here, {{{x}}} is raw)."),
        from_starter: z
          .string()
          .optional()
          .describe(
            "Starter alias to copy verbatim (e.g. welcome, approval-request). Mutually exclusive with subject/body/html_body.",
          ),
      }),
    },
    async (args) =>
      runTool(() =>
        // Only what the caller passed reaches the wire — a fabricated
        // subject/body key would trip the server's from_starter exclusivity.
        client.createTemplate({
          ...(args.name !== undefined ? { name: args.name } : {}),
          ...(args.alias !== undefined ? { alias: args.alias } : {}),
          ...(args.subject !== undefined ? { subject: args.subject } : {}),
          ...(args.body !== undefined ? { body: args.body } : {}),
          ...(args.html_body !== undefined ? { htmlBody: args.html_body } : {}),
          ...(args.from_starter !== undefined ? { fromStarter: args.from_starter } : {}),
        }),
      ),
  );

  server.registerTool(
    "update_template",
    {
      title: "Update an email template (beta)",
      annotations: { idempotentHint: true, destructiveHint: false },
      description:
        "Partial update of a stored template. Fields you do NOT pass are left unchanged; changed template parts are " +
        're-parsed (bad syntax rejects the update). Set alias or html_body to "" to clear them. Sends already in flight ' +
        "are unaffected — rendering happens at send time, so future sends pick up the new source. " +
        SYNTAX + " " + BETA,
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("Template id (tmpl_…)."),
        name: z.string().optional(),
        alias: z.string().optional().describe('Set to "" to clear the alias.'),
        subject: z.string().optional(),
        body: z.string().optional(),
        html_body: z.string().optional().describe('Set to "" to remove the HTML part.'),
      }),
    },
    async (args) =>
      runTool(() =>
        client.updateTemplate(args.id, {
          ...(args.name !== undefined ? { name: args.name } : {}),
          ...(args.alias !== undefined ? { alias: args.alias } : {}),
          ...(args.subject !== undefined ? { subject: args.subject } : {}),
          ...(args.body !== undefined ? { body: args.body } : {}),
          // An explicit "" is a deliberate clear — it must survive to the wire.
          ...(args.html_body !== undefined ? { htmlBody: args.html_body } : {}),
        }),
      ),
  );

  server.registerTool(
    "delete_template",
    {
      title: "Delete an email template (DESTRUCTIVE, beta)",
      annotations: { destructiveHint: true, idempotentHint: true },
      description:
        "Permanently delete a stored template. In-flight sends are unaffected (rendering happens at send time), but " +
        "future sends referencing its id or alias will fail. Requires confirm:true so an LLM cannot delete on " +
        "ambiguous context. " + BETA,
      inputSchema: strictInputSchema({
        id: z.string().min(1).describe("Template id (tmpl_…)."),
        confirm: z.literal(true).describe("Must be true to proceed."),
      }),
    },
    async (args) =>
      runTool(async () => {
        if (args.confirm !== true) {
          throw new Error("delete_template requires confirm:true.");
        }
        await client.deleteTemplate(args.id);
        return { deleted: args.id };
      }),
  );

  server.registerTool(
    "validate_template",
    {
      title: "Validate template source (dry run, beta)",
      annotations: { readOnlyHint: true },
      description:
        "Dry-run template source WITHOUT persisting anything: returns per-part parse errors, a rendered preview " +
        "against test_data (present only when valid), and suggestedData — a placeholder value for every variable the " +
        "source references, nested along dot paths (a ready-made starting point for template_data). Use this before " +
        "`create_template` to catch syntax errors, and to spot silent blanks: a missing variable renders as empty " +
        "string, so preview with realistic test_data and check the output. " + SYNTAX + " " + BETA,
      inputSchema: strictInputSchema({
        subject: z.string().optional().describe("Subject source to validate."),
        body: z.string().optional().describe("Plain-text body source to validate."),
        html_body: z.string().optional().describe("HTML body source to validate."),
        test_data: z
          .record(z.string(), z.unknown())
          .optional()
          .describe("Sample template_data to render the preview with. Missing variables render as empty strings."),
      }),
    },
    async (args) =>
      runTool(() =>
        client.validateTemplate({
          ...(args.subject !== undefined ? { subject: args.subject } : {}),
          ...(args.body !== undefined ? { body: args.body } : {}),
          ...(args.html_body !== undefined ? { htmlBody: args.html_body } : {}),
          ...(args.test_data !== undefined ? { testData: args.test_data } : {}),
        }),
      ),
  );

  server.registerTool(
    "list_starter_templates",
    {
      title: "List starter templates (beta)",
      annotations: { readOnlyHint: true },
      description:
        "List the pre-built starter templates shipped with the deployment (welcome, verify-code, password-reset, " +
        "receipt, agent-status, daily-digest, approval-request), with each one's variables — name, required?, raw?, " +
        "description, example. Catalog metadata only; `get_starter_template` adds the full body sources. Copy one into " +
        "the account's library with `create_template`'s from_starter, then send with template_alias. Variables marked " +
        "raw:true are {{{…_html}}} fragment slots — HTML-escape any user content you splice into them. " +
        APPROVAL_LINK_WARNING + " " + BETA,
      inputSchema: strictInputSchema({}),
    },
    async () => runTool(async () => ({ starter_templates: await client.listStarterTemplates() })),
  );

  server.registerTool(
    "get_starter_template",
    {
      title: "Get a starter template (beta)",
      annotations: { readOnlyHint: true },
      description:
        "Fetch one starter template by alias, including its full plain-text and HTML body sources and its variables " +
        "(each with a realistic example value usable as test_data). Starters are read-only masters: to customize one, " +
        "copy it with `create_template` {from_starter: alias} and edit the copy. Variables with raw:true are " +
        "{{{…_html}}} fragment slots — the caller supplies pre-rendered HTML and MUST escape user content in it. " +
        APPROVAL_LINK_WARNING + " " + BETA,
      inputSchema: strictInputSchema({
        alias: z.string().min(1).describe("The starter template's alias, e.g. welcome or approval-request."),
      }),
    },
    async (args) => runTool(() => client.getStarterTemplate(args.alias)),
  );
}
