import { randomBytes } from "node:crypto";
import type { NextFunction, Request, Response } from "express";

// Inbound ids are honored only when they match a safe, bounded alphabet —
// the value is reflected into logs and JSON-RPC error bodies, so anything
// exotic (control chars, huge strings) is discarded and replaced.
const INBOUND_REQUEST_ID = /^[A-Za-z0-9_-]{1,64}$/;

export function mintRequestId(): string {
  return `mcpreq_${randomBytes(6).toString("hex")}`;
}

/**
 * Correlation middleware: attach a request id to every request and echo it
 * back as X-Request-Id on every response (the header is set eagerly, so it
 * rides 401/405/500 responses too). The id lives on res.locals and is what
 * the request-scoped log events and JSON-RPC error `data.request_id` fields
 * carry.
 */
export function correlationMiddleware(req: Request, res: Response, next: NextFunction): void {
  const inbound = req.headers["x-request-id"];
  const id =
    typeof inbound === "string" && INBOUND_REQUEST_ID.test(inbound) ? inbound : mintRequestId();
  res.locals.requestId = id;
  res.setHeader("X-Request-Id", id);
  next();
}

/** The request id attached by {@link correlationMiddleware}. */
export function requestIdOf(res: Response): string {
  return typeof res.locals.requestId === "string" ? res.locals.requestId : "unknown";
}
