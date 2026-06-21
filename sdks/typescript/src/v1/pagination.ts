// Auto-pagination for the e2a SDK (Slice 8b-1).
//
// The /v1 list endpoints return `{ items, next_cursor }`. AutoPager turns a
// page-fetch function into an async iterable, so a caller writes
// `for await (const m of client.messages.list()) { ... }` and the cursor is
// threaded for them — with a guard against a non-advancing cursor (which would
// otherwise loop forever).

export interface Page<T> {
  items: T[];
  /** null / undefined / "" → no more pages. */
  next_cursor?: string | null;
}

export type FetchPage<T> = (cursor: string | undefined) => Promise<Page<T>>;

export interface AutoPagerOptions {
  /** Hard ceiling on pages fetched, to bound a server that returns an
   *  ever-advancing (never-repeating, never-null) cursor — which the
   *  repeated-cursor guard alone can't catch. Default 10000. */
  maxPages?: number;
}

export class AutoPager<T> implements AsyncIterable<T> {
  private readonly maxPages: number;
  constructor(
    private readonly fetchPage: FetchPage<T>,
    opts: AutoPagerOptions = {},
  ) {
    this.maxPages = opts.maxPages ?? 10000;
  }

  async *[Symbol.asyncIterator](): AsyncIterator<T> {
    let cursor: string | undefined;
    // Every cursor we've already requested. A repeat means the server is
    // cycling (A-B-A or A-B-C-A-…); bail rather than loop. Bounded by maxPages.
    const seen = new Set<string>();
    let pages = 0;
    for (;;) {
      if (pages >= this.maxPages) {
        throw new Error(
          `e2a pagination: exceeded ${this.maxPages} pages; aborting (cursor never terminated)`,
        );
      }
      const page = await this.fetchPage(cursor);
      pages += 1;
      for (const item of page.items ?? []) yield item;

      const next = page.next_cursor ?? undefined;
      if (!next) return; // null / empty → the last page
      if (next === cursor || seen.has(next)) {
        throw new Error(
          "e2a pagination: next_cursor did not advance; aborting to avoid an infinite loop",
        );
      }
      if (cursor !== undefined) seen.add(cursor);
      cursor = next;
    }
  }

  /** Fetch a SINGLE page for caller-driven pagination: pass the previous
   *  page's `next_cursor` (omit for the first page) and get back `{ items,
   *  next_cursor }`. A null/undefined/empty `next_cursor` in the result means
   *  there are no more pages. This is the primitive behind the cursor+limit
   *  shape the MCP tools expose (§6a #3) and is available to SDK users who want
   *  manual paging instead of `for await` / `toArray`. The page size is governed
   *  by the `limit` baked into the list call that produced this pager. */
  async page(cursor?: string): Promise<Page<T>> {
    const p = await this.fetchPage(cursor || undefined);
    // Normalize null/undefined/"" → undefined (= no more pages), matching the
    // iterator's own `!next` termination and this method's docstring. A bare
    // `?? undefined` would leak an empty-string cursor as a truthy "more pages".
    const next = p.next_cursor;
    return { items: p.items ?? [], next_cursor: next ? next : undefined };
  }

  /** Collect up to `limit` items. The limit is required — it caps memory for
   *  an inbox that could page indefinitely. */
  async toArray(opts: { limit: number }): Promise<T[]> {
    if (!Number.isFinite(opts.limit) || opts.limit <= 0) {
      throw new Error("e2a pagination: toArray requires a positive limit");
    }
    const out: T[] = [];
    for await (const item of this) {
      out.push(item);
      if (out.length >= opts.limit) break;
    }
    return out;
  }

  /** Invoke `fn` per item; return `false` from `fn` to stop early. */
  async forEach(fn: (item: T) => boolean | void | Promise<boolean | void>): Promise<void> {
    for await (const item of this) {
      if ((await fn(item)) === false) return;
    }
  }
}
