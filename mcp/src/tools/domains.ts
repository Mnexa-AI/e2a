import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { E2AClient } from "@e2a/sdk/v1";
import { z } from "zod";
import { runTool, strictInputSchema } from "./util.js";

/**
 * Domain-management tools. Mirrors the four domain endpoints the
 * SDK exposes. `register_domain` is the headline composition point:
 * it returns the MX + TXT records the user must publish, and the
 * docstring tells the model to chain into a DNS-provider MCP
 * (Cloudflare, Route 53, …) to actually create those records — then
 * loop back to `verify_domain` once propagation completes.
 *
 * `delete_domain` mirrors `delete_agent`: a Zod `literal(true)`
 * confirm gate so the schema validator catches LLM hallucinations
 * before any HTTP call runs.
 */
export function registerDomainTools(server: McpServer, client: E2AClient): void {
  server.registerTool(
    "list_domains",
    {
      title: "List domains",
      description:
        "List every custom mail domain registered under the authenticated user, with verification status (verified / pending DNS / failed) and the verification token for each. Useful to discover which domains can already send/receive mail and which still need DNS records to be added. Read-only; cheap to call.",
      inputSchema: strictInputSchema({}),
    },
    async () => runTool(() => client.listDomains()),
  );

  server.registerTool(
    "register_domain",
    {
      title: "Register a custom mail domain (returns DNS records to publish)",
      description:
        "Use to start the custom-domain flow. **This is step 1 of an asynchronous two-step process**: this tool returns the MX + TXT records the user must publish on their DNS provider; it does NOT make the domain live. Step 2 is `verify_domain` — but only AFTER the records are published AND DNS propagation has completed (typically minutes, occasionally hours). Do not call `verify_domain` immediately, and do not promise the user the domain works yet. **Composition tip**: if a DNS-provider MCP (Cloudflare, Route 53, NS1, …) is loaded in the same host, hand the returned records to its `create_dns_record`-style tool, then surface the wait expectation to the user. If no DNS MCP is available, show the records verbatim and ask the user to add them manually. The domain is created in an unverified state — `delete_domain` is the reverse op. Idempotent against an existing-but-unverified row.",
      inputSchema: strictInputSchema({
        domain: z
          .string()
          .min(1)
          .describe(
            "Fully-qualified domain name to register. e.g. 'mail.acme.com'. Subdomains are recommended (so the apex remains free for marketing mail).",
          ),
      }),
    },
    async (args) => runTool(() => client.registerDomain(args.domain)),
  );

  server.registerTool(
    "verify_domain",
    {
      title: "Verify a custom mail domain's DNS records",
      description:
        "Use as step 2 of the custom-domain flow, AFTER `register_domain` returned records AND the user (or a DNS MCP) published them AND DNS has had time to propagate (minutes to hours). Probes DNS for the issued MX + TXT records and flips the domain's `verified` bit on success. Idempotent — safe to retry as propagation completes. If `verified: false` comes back, the response includes the resolved-record state for diagnostics; surface that to the user rather than retrying in a tight loop. Don't poll; let the user drive the recheck.",
      inputSchema: strictInputSchema({
        domain: z
          .string()
          .min(1)
          .describe("Domain to verify. Must have been registered already via `register_domain`."),
      }),
    },
    async (args) => runTool(() => client.verifyDomain(args.domain)),
  );

  server.registerTool(
    "delete_domain",
    {
      title: "Delete a custom mail domain (DESTRUCTIVE)",
      description:
        "Permanently remove a domain registration. CASCADES to every agent on that domain and every message/pending-outbound/webhook-delivery bound to those agents. Irreversible. Existing OAuth tokens bound to those agents are revoked. Requires `confirm: true` — set it explicitly to acknowledge the destructive scope.",
      inputSchema: strictInputSchema({
        domain: z.string().min(1).describe("Domain to delete."),
        confirm: z
          .literal(true)
          .describe(
            "Must be set to true to proceed. Guard against an LLM hallucinating a delete from ambiguous context.",
          ),
      }),
    },
    async (args) =>
      runTool(async () => {
        if (args.confirm !== true) {
          throw new Error(
            "delete_domain requires confirm:true — refusing to proceed without explicit confirmation.",
          );
        }
        await client.deleteDomain(args.domain);
        return { deleted: args.domain };
      }),
  );
}
