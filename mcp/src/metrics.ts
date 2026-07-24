/**
 * Dependency-free in-process metrics registry for the MCP HTTP transport.
 *
 * Counters and a fixed-bucket request-duration histogram, rendered in the
 * Prometheus text exposition format for `GET /metrics`. Label values come
 * from closed vocabularies (route names, result/outcome tokens, tool names),
 * so cardinality stays bounded; no bearer, user, or request data ever lands
 * in a label. Node's single-threaded event loop makes the increments safe
 * without locking.
 *
 * The registry is injectable into {@link buildApp} (`HttpServerOptions
 * .metrics`) so tests can assert against an isolated instance; the server
 * creates a fresh one per app otherwise.
 */

export type RouteLabel = "mcp" | "healthz" | "readyz" | "metrics" | "discovery" | "other";
export type AuthResolutionResult = "cache_hit" | "resolved" | "invalid" | "fallback";
export type ToolOutcome = "ok" | "error";
export type ReadyzResult = "ok" | "degraded";

// Fixed histogram buckets (seconds), covering fast health checks up to a
// slow cold whoami probe.
const REQUEST_DURATION_BUCKETS = [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10] as const;

type LabelSet = Record<string, string>;

interface CounterSeries {
  labels: LabelSet;
  value: number;
}

interface HistogramSeries {
  route: string;
  /** Cumulative counts aligned with REQUEST_DURATION_BUCKETS. */
  buckets: number[];
  sum: number;
  count: number;
}

// Map key for a label set: sorted, NUL-joined (NUL can't appear in our
// closed label vocabularies).
function labelKey(labels: LabelSet): string {
  return Object.keys(labels)
    .sort()
    .map((k) => `${k}=${labels[k]}`)
    .join("\x00");
}

function escapeLabelValue(value: string): string {
  return value.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n");
}

function renderLabels(labels: LabelSet): string {
  const keys = Object.keys(labels).sort();
  if (keys.length === 0) return "";
  return `{${keys.map((k) => `${k}="${escapeLabelValue(labels[k]!)}"`).join(",")}}`;
}

function inc(map: Map<string, CounterSeries>, labels: LabelSet): void {
  const key = labelKey(labels);
  const series = map.get(key);
  if (series) {
    series.value += 1;
  } else {
    map.set(key, { labels, value: 1 });
  }
}

export class MetricsRegistry {
  private readonly httpRequests = new Map<string, CounterSeries>();
  private readonly authResolutions = new Map<string, CounterSeries>();
  private readonly toolExecutions = new Map<string, CounterSeries>();
  private readonly readyzChecks = new Map<string, CounterSeries>();
  private readonly durations = new Map<string, HistogramSeries>();
  private resolveCacheEntries = 0;

  incHttpRequest(route: RouteLabel, statusClass: string): void {
    inc(this.httpRequests, { route, status_class: statusClass });
  }

  incAuthResolution(result: AuthResolutionResult): void {
    inc(this.authResolutions, { result });
  }

  incToolExecution(tool: string, outcome: ToolOutcome): void {
    inc(this.toolExecutions, { tool, outcome });
  }

  incReadyzCheck(result: ReadyzResult): void {
    inc(this.readyzChecks, { result });
  }

  observeRequestDuration(route: RouteLabel, seconds: number): void {
    let series = this.durations.get(route);
    if (!series) {
      series = {
        route,
        buckets: REQUEST_DURATION_BUCKETS.map(() => 0),
        sum: 0,
        count: 0,
      };
      this.durations.set(route, series);
    }
    for (let i = 0; i < REQUEST_DURATION_BUCKETS.length; i++) {
      if (seconds <= REQUEST_DURATION_BUCKETS[i]!) series.buckets[i]! += 1;
    }
    series.sum += seconds;
    series.count += 1;
  }

  setResolveCacheEntries(n: number): void {
    this.resolveCacheEntries = n;
  }

  reset(): void {
    this.httpRequests.clear();
    this.authResolutions.clear();
    this.toolExecutions.clear();
    this.readyzChecks.clear();
    this.durations.clear();
    this.resolveCacheEntries = 0;
  }

  /** Render the Prometheus text exposition format (v0.0.4). */
  render(): string {
    const lines: string[] = [];
    const emitCounter = (name: string, help: string, map: Map<string, CounterSeries>) => {
      lines.push(`# HELP ${name} ${help}`, `# TYPE ${name} counter`);
      for (const series of map.values()) {
        lines.push(`${name}${renderLabels(series.labels)} ${series.value}`);
      }
    };
    emitCounter("mcp_http_requests_total", "HTTP requests by route and status class.", this.httpRequests);
    emitCounter(
      "mcp_auth_resolutions_total",
      "Bearer-to-principal whoami resolutions by result.",
      this.authResolutions,
    );
    emitCounter("mcp_tool_executions_total", "MCP tool executions by tool and outcome.", this.toolExecutions);
    emitCounter("mcp_readyz_checks_total", "Readiness probe checks by result.", this.readyzChecks);

    lines.push(
      "# HELP mcp_http_request_duration_seconds HTTP request duration in seconds.",
      "# TYPE mcp_http_request_duration_seconds histogram",
    );
    for (const series of this.durations.values()) {
      const route = escapeLabelValue(series.route);
      series.buckets.forEach((count, i) => {
        lines.push(
          `mcp_http_request_duration_seconds_bucket{route="${route}",le="${REQUEST_DURATION_BUCKETS[i]}"} ${count}`,
        );
      });
      lines.push(`mcp_http_request_duration_seconds_bucket{route="${route}",le="+Inf"} ${series.count}`);
      lines.push(`mcp_http_request_duration_seconds_sum{route="${route}"} ${series.sum}`);
      lines.push(`mcp_http_request_duration_seconds_count{route="${route}"} ${series.count}`);
    }

    lines.push(
      "# HELP mcp_resolve_cache_entries Bearer-to-principal cache entries currently held.",
      "# TYPE mcp_resolve_cache_entries gauge",
      `mcp_resolve_cache_entries ${this.resolveCacheEntries}`,
    );
    return lines.join("\n") + "\n";
  }
}
