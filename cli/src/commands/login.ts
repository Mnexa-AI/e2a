import { execFile } from "node:child_process";
import { randomBytes } from "node:crypto";
import { createServer, type IncomingMessage, type Server } from "node:http";
import { loadConfig, saveConfig } from "../config.js";

const LOGIN_TIMEOUT_MS = 2 * 60 * 1000;

function openBrowser(url: string): void {
  if (process.platform === "win32") {
    const child = execFile("cmd", ["/c", "start", "", url], (err) => {
      if (err) {
        process.stderr.write(`Could not open browser. Visit manually: ${url}\n`);
      }
    });
    child.unref();
    return;
  }

  const cmd = process.platform === "darwin" ? "open" : "xdg-open";
  const child = execFile(cmd, [url], (err) => {
    if (err) {
      process.stderr.write(`Could not open browser. Visit manually: ${url}\n`);
    }
  });
  child.unref();
}

function renderCallbackPage(title: string, message: string): string {
  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${title}</title>
  <style>
    body {
      margin: 0;
      font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: #f5f7fb;
      color: #111827;
      display: grid;
      place-items: center;
      min-height: 100vh;
      padding: 24px;
    }
    main {
      width: 100%;
      max-width: 460px;
      background: white;
      border: 1px solid #e5e7eb;
      border-radius: 16px;
      padding: 28px;
      box-shadow: 0 18px 50px rgba(15, 23, 42, 0.08);
    }
    h1 { margin: 0 0 10px; font-size: 24px; }
    p { margin: 0; color: #4b5563; line-height: 1.5; }
  </style>
</head>
<body>
  <main>
    <h1>${title}</h1>
    <p>${message}</p>
  </main>
</body>
</html>`;
}

function readRequestBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    let body = "";
    req.setEncoding("utf8");
    req.on("data", (chunk) => {
      body += chunk;
    });
    req.on("end", () => resolve(body));
    req.on("error", reject);
  });
}

function closeServer(server: Server): Promise<void> {
  return new Promise((resolve) => {
    if (!server.listening) {
      resolve();
      return;
    }
    server.close(() => resolve());
  });
}

function buildBrowserLoginURL(apiUrl: string, callbackUrl: string, cliState: string): string {
  const loginUrl = new URL("/api/auth/login", apiUrl);
  loginUrl.searchParams.set("cli_callback", callbackUrl);
  loginUrl.searchParams.set("cli_state", cliState);
  return loginUrl.toString();
}

type BrowserLoginResult = {
  apiKey: string;
  agentEmail: string;
};

async function waitForBrowserLogin(apiUrl: string, timeoutMs = LOGIN_TIMEOUT_MS): Promise<BrowserLoginResult> {
  const cliState = randomBytes(16).toString("hex");

  let settled = false;
  let server: Server | null = null;

  const result = new Promise<BrowserLoginResult>((resolve, reject) => {
    const finish = async (value: BrowserLoginResult | Error, isError: boolean) => {
      if (settled) return;
      settled = true;
      if (server) {
        await closeServer(server);
      }
      if (isError) reject(value);
      else resolve(value as BrowserLoginResult);
    };

    server = createServer(async (req, res) => {
      res.setHeader("Connection", "close");
      try {
        if (req.method !== "POST") {
          res.statusCode = 405;
          res.setHeader("Content-Type", "text/html; charset=utf-8");
          res.end(renderCallbackPage("Return to your terminal", "Run e2a login again to finish connecting the CLI."));
          return;
        }

        const body = await readRequestBody(req);
        const params = new URLSearchParams(body);
        const returnedState = params.get("cli_state") || "";
        const apiKey = params.get("api_key") || "";
        const agentEmail = params.get("agent_email") || "";
        const error = params.get("error") || "";

        if (returnedState != cliState) {
          res.statusCode = 400;
          res.setHeader("Content-Type", "text/html; charset=utf-8");
          res.end(renderCallbackPage("e2a login failed", "The browser login state did not match this terminal session. Return to the terminal and try again."));
          await finish(new Error("browser login state mismatch"), true);
          return;
        }

        if (error) {
          res.statusCode = 400;
          res.setHeader("Content-Type", "text/html; charset=utf-8");
          res.end(renderCallbackPage("e2a login failed", error));
          await finish(new Error(error), true);
          return;
        }

        if (!apiKey.startsWith("e2a_")) {
          res.statusCode = 400;
          res.setHeader("Content-Type", "text/html; charset=utf-8");
          res.end(renderCallbackPage("e2a login failed", "The browser did not return a valid API key."));
          await finish(new Error("browser login did not return a valid api key"), true);
          return;
        }

        res.statusCode = 200;
        res.setHeader("Content-Type", "text/html; charset=utf-8");
        res.end(renderCallbackPage("e2a CLI connected", "You can return to the terminal. Your config has been updated automatically."));

        await finish({ apiKey, agentEmail }, false);
      } catch (error) {
        res.statusCode = 500;
        res.setHeader("Content-Type", "text/html; charset=utf-8");
        res.end(renderCallbackPage("e2a login failed", "The CLI could not process the browser callback."));
        await finish(error instanceof Error ? error : new Error("failed to process browser callback"), true);
      }
    });

    server.on("error", async (error) => {
      await finish(error instanceof Error ? error : new Error("browser login server failed"), true);
    });

    server.listen(0, "127.0.0.1", () => {
      const address = server?.address();
      if (!address || typeof address === "string") {
        void finish(new Error("failed to start local login callback"), true);
        return;
      }

      const callbackUrl = `http://127.0.0.1:${address.port}/callback`;
      const loginUrl = buildBrowserLoginURL(apiUrl, callbackUrl, cliState);

      process.stderr.write("\n");
      process.stderr.write("Opening e2a.dev in your browser...\n");
      process.stderr.write("Log in to e2a to finish connecting this CLI.\n");
      process.stderr.write("\n");

      openBrowser(loginUrl);
    });
  });

  const timeout = setTimeout(async () => {
    if (settled) return;
    settled = true;
    if (server) {
      await closeServer(server);
    }
  }, timeoutMs);

  try {
    return await Promise.race([
      result,
      new Promise<BrowserLoginResult>((_, reject) => {
        setTimeout(() => reject(new Error("timed out waiting for browser login")), timeoutMs);
      }),
    ]);
  } finally {
    clearTimeout(timeout);
    if (server) {
      await closeServer(server);
    }
  }
}

export async function login(): Promise<void> {
  const config = loadConfig();
  const { apiKey, agentEmail } = await waitForBrowserLogin(config.api_url);

  saveConfig({ api_key: apiKey, agent_email: agentEmail });

  process.stdout.write("Logged in. Config saved to ~/.e2a/config.json\n");
  if (agentEmail) {
    process.stdout.write(`Active agent: ${agentEmail}\n`);
  } else {
    process.stderr.write("No agents found yet. Run: e2a agents register <slug>\n");
  }
}
