import type { CreateAPIKeyRequest } from "@e2a/sdk/v1";
import { hostname } from "node:os";
import { createClient } from "../sdk.js";
import { EXIT, fail } from "../exit.js";

export interface KeysCreateOptions {
  name?: string;
  /** Bind the key to this inbox (scope=agent). Omit for an account-scoped key. */
  agent?: string;
  json?: boolean;
}

export interface KeysListOptions {
  json?: boolean;
}

const DELETE_USAGE = "usage: e2a keys delete <key-id>";

// Account-scope only. `create --agent` is the non-interactive path to a
// least-privilege agent key — what a harness bootstrap (tether setup) runs
// when the account credential already exists and no browser is available.
export async function keysCreate(opts: KeysCreateOptions): Promise<void> {
  const client = createClient();
  const created = await client.account.apiKeys.create({
    name: opts.name || (opts.agent ? `agent key @${hostname()}` : `account key @${hostname()}`),
    ...(opts.agent
      ? { scope: "agent" as CreateAPIKeyRequest["scope"], agentEmail: opts.agent }
      : {}),
  });

  if (opts.json) {
    process.stdout.write(JSON.stringify(created) + "\n");
    return;
  }
  // Plaintext key alone on stdout (script-friendly: KEY=$(e2a keys create …));
  // the shown-once warning goes to stderr so it can't pollute the capture.
  process.stdout.write(created.key + "\n");
  process.stderr.write(
    `Key ${created.id} created (${opts.agent ? `agent-scoped: ${opts.agent}` : "account-scoped"}). Shown once — store it now.\n`,
  );
}

export async function keysList(opts: KeysListOptions): Promise<void> {
  const client = createClient();
  for await (const k of client.account.apiKeys.list()) {
    if (opts.json) {
      process.stdout.write(JSON.stringify(k) + "\n");
    } else {
      process.stdout.write(
        `${k.id}\t${k.keyPrefix}\t${k.scope}${k.agentEmail ? `\t${k.agentEmail}` : "\t"}\t${k.name || ""}\n`,
      );
    }
  }
}

export async function keysDelete(id: string | undefined): Promise<void> {
  if (!id) fail(EXIT.USAGE, DELETE_USAGE);
  const client = createClient();
  // The API confirms deletes with a 200 + deletion object ({deleted, id});
  // echo the server's confirmation rather than the caller's input.
  const res = await client.account.apiKeys.delete(id);
  process.stdout.write(`revoked ${res.id}\n`);
}
