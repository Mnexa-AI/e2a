import { createClient } from "../sdk.js";

export async function domainsList(): Promise<void> {
  const client = createClient();

  const domains = await client.domains.list().toArray({ limit: 1000 });

  if (domains.length === 0) {
    process.stderr.write("No domains registered. Run: e2a domains register <domain>\n");
    return;
  }

  for (const d of domains) {
    const verified = d.verified ? "verified" : "unverified";
    process.stdout.write(`${d.domain}  ${verified}\n`);
  }
}

export async function domainsRegister(domain: string | undefined): Promise<void> {
  if (!domain) {
    process.stderr.write("Usage: e2a domains register <domain>\n");
    process.stderr.write("Example: e2a domains register mycompany.com\n");
    process.exit(1);
  }

  const client = createClient();

  const res = await client.domains.create({ domain });

  process.stdout.write(`Registered: ${res.domain}\n`);

  if (res.dnsRecords) {
    process.stdout.write("\nAdd these DNS records to verify ownership:\n\n");
    const mx = res.dnsRecords.mx;
    const txt = res.dnsRecords.txt;
    if (mx) {
      process.stdout.write(`  MX  ${mx.host}  ${mx.value}  (priority ${mx.priority ?? 10})\n`);
    }
    if (txt) {
      process.stdout.write(`  TXT  ${txt.host}  ${txt.value}\n`);
    }
    process.stdout.write("\nThen run: e2a domains verify " + domain + "\n");
  }
}

export async function domainsVerify(domain: string | undefined): Promise<void> {
  if (!domain) {
    process.stderr.write("Usage: e2a domains verify <domain>\n");
    process.exit(1);
  }

  const client = createClient();

  const res = await client.domains.verify(domain);

  if (res.verified) {
    process.stdout.write(`Verified: ${res.domain}\n`);
  } else {
    process.stderr.write(`Not yet verified: ${res.domain}\n`);
    process.stderr.write("Check your DNS records and try again.\n");
  }
}

export async function domainsDelete(domain: string | undefined): Promise<void> {
  if (!domain) {
    process.stderr.write("Usage: e2a domains delete <domain>\n");
    process.exit(1);
  }

  const client = createClient();

  await client.domains.delete(domain);

  process.stdout.write(`Deleted: ${domain}\n`);
}
