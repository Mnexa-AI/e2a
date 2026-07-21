import "dotenv/config";

import { pathToFileURL } from "node:url";

import express, { type Express, type NextFunction, type Request, type Response } from "express";
import { E2AClient, E2AWebhookSignatureError } from "@e2a/sdk/v1";

import {
  createADKReplyAgent,
  createAnthropicReplyAgent,
  createLangChainReplyAgent,
  createOpenAIReplyAgent,
  FakeReplyAgent,
} from "./adapters/index.js";
import type { InboundResource, ReplyAgent } from "./contracts.js";
import { EventDeduper } from "./delivery-state.js";
import { DeliveryInProgress, handleDelivery } from "./handler.js";

export const SUPPORTED_FRAMEWORKS = ["openai", "anthropic", "langchain", "adk", "fake"] as const;
export const MAX_WEBHOOK_BODY_BYTES = 1024 * 1024;
export type Framework = (typeof SUPPORTED_FRAMEWORKS)[number];
type Environment = Record<string, string | undefined>;
type AgentFactories = Partial<Record<Framework, () => ReplyAgent>>;

class ConfigurationError extends Error {}

function required(name: string, env: Environment): string {
  const value = env[name];
  if (!value) throw new ConfigurationError(`${name} is required`);
  return value;
}

function isFramework(value: string): value is Framework {
  return (SUPPORTED_FRAMEWORKS as readonly string[]).includes(value);
}

export function validateProviderConfig(framework: Framework, env: Environment): void {
  if (framework === "fake") return;
  if (framework === "openai") return void required("OPENAI_API_KEY", env);
  if (framework === "anthropic") return void required("ANTHROPIC_API_KEY", env);
  if (framework === "langchain") {
    const model = env.LANGCHAIN_MODEL ?? "openai:gpt-5.4";
    if (!model.startsWith("openai:")) {
      throw new ConfigurationError("LANGCHAIN_MODEL must use the installed openai: provider prefix");
    }
    return void required("OPENAI_API_KEY", env);
  }
  if (env.GEMINI_API_KEY || env.GOOGLE_API_KEY) return;
  if (env.GOOGLE_GENAI_USE_VERTEXAI?.toLowerCase() === "true") {
    required("GOOGLE_CLOUD_PROJECT", env);
    required("GOOGLE_CLOUD_LOCATION", env);
    return;
  }
  throw new ConfigurationError("ADK requires GEMINI_API_KEY or GOOGLE_API_KEY, or complete Vertex config");
}

function defaultFactories(env: Environment): Record<Framework, () => ReplyAgent> {
  return {
    openai: () => createOpenAIReplyAgent(env),
    anthropic: () => createAnthropicReplyAgent(env),
    langchain: () => createLangChainReplyAgent(env),
    adk: () => createADKReplyAgent(env),
    fake: () => new FakeReplyAgent("Deterministic fake reply"),
  };
}

export function selectAgent(
  framework: string,
  factories: AgentFactories = defaultFactories(process.env),
): ReplyAgent {
  if (!isFramework(framework) || !factories[framework]) {
    throw new ConfigurationError(`AGENT_FRAMEWORK must be one of: ${SUPPORTED_FRAMEWORKS.join(", ")}`);
  }
  return factories[framework]();
}

interface AppOptions {
  env?: Environment;
  framework?: string;
  apiKey?: string;
  webhookSecret?: string;
  baseUrl?: string;
  inbound?: InboundResource;
  agent?: ReplyAgent;
  deduper?: EventDeduper;
  agentFactories?: AgentFactories;
}

interface Runtime {
  secret: string;
  inbound: InboundResource;
  agent: ReplyAgent;
  deduper: EventDeduper;
  closed: boolean;
}

const runtimes = new WeakMap<Express, Runtime>();

function limitedRawBody(request: Request, response: Response, next: NextFunction): void {
  const declared = Number(request.headers["content-length"]);
  let oversized = Number.isFinite(declared) && declared > MAX_WEBHOOK_BODY_BYTES;
  let size = 0;
  const chunks: Buffer[] = [];
  request.on("data", (chunk: Buffer) => {
    size += chunk.byteLength;
    if (size > MAX_WEBHOOK_BODY_BYTES) oversized = true;
    if (!oversized) chunks.push(chunk);
  });
  request.on("end", () => {
    if (oversized) {
      response.status(413).json({ error: "webhook body too large" });
      return;
    }
    request.body = Buffer.concat(chunks, size);
    next();
  });
  request.on("error", next);
}

function initialize(options: AppOptions): Runtime {
  const env = options.env ?? process.env;
  const selected = options.framework ?? env.AGENT_FRAMEWORK ?? "fake";
  if (!isFramework(selected)) {
    throw new ConfigurationError(`AGENT_FRAMEWORK must be one of: ${SUPPORTED_FRAMEWORKS.join(", ")}`);
  }
  const secret = options.webhookSecret ?? required("E2A_WEBHOOK_SECRET", env);
  if (options.inbound && options.agent) {
    return { secret, inbound: options.inbound, agent: options.agent, deduper: options.deduper ?? new EventDeduper(), closed: false };
  }
  validateProviderConfig(selected, env);
  const client = new E2AClient({
    apiKey: options.apiKey ?? required("E2A_API_KEY", env),
    ...(options.baseUrl === undefined ? {} : { baseUrl: options.baseUrl }),
  });
  const agent = selectAgent(selected, options.agentFactories ?? defaultFactories(env));
  return { secret, inbound: client.inbound, agent, deduper: options.deduper ?? new EventDeduper(), closed: false };
}

export function createApp(options: AppOptions = {}): Express {
  const app = express();
  let runtime: Runtime | undefined;
  let startupDetail: string | undefined;
  try {
    runtime = initialize(options);
    runtimes.set(app, runtime);
  } catch (error: unknown) {
    runtime = undefined;
    startupDetail = error instanceof ConfigurationError
      ? error.message
      : "runtime initialization failed";
  }

  app.get("/health", (_request, response) => {
    if (!runtime) return response.status(503).json({ status: "unavailable", detail: startupDetail });
    return response.json({ status: "ok" });
  });

  app.post(
    "/webhook",
    limitedRawBody,
    async (request: Request, response: Response) => {
      if (!runtime) return response.status(503).json({ error: "runtime unavailable" });
      try {
        const result = await handleDelivery({
          body: request.body as Buffer,
          signature: request.header("X-E2A-Signature") ?? "",
          secret: runtime.secret,
          inbound: runtime.inbound,
          agent: runtime.agent,
          deduper: runtime.deduper,
        });
        return response.json(result);
      } catch (error: unknown) {
        if (error instanceof E2AWebhookSignatureError) {
          return response.status(401).json({ error: "invalid signature" });
        }
        if (error instanceof DeliveryInProgress) {
          return response.status(503).json({ error: "delivery in progress" });
        }
        return response.status(500).json({ error: "delivery failed" });
      }
    },
  );

  return app;
}

export async function closeApp(app: Express): Promise<void> {
  const runtime = runtimes.get(app);
  if (!runtime || runtime.closed) return;
  runtime.closed = true;
  await runtime.agent.close?.();
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const app = createApp();
  const server = app.listen(Number(process.env.PORT ?? 8000), "0.0.0.0");
  const shutdown = async () => {
    await new Promise<void>((resolve, reject) => {
      server.close((error) => error ? reject(error) : resolve());
    });
    await closeApp(app);
  };
  process.once("SIGINT", () => void shutdown());
  process.once("SIGTERM", () => void shutdown());
}
