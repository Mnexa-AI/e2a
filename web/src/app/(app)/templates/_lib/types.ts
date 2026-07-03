// Shared view types for the templates surface. These mirror the /v1
// schemas in api/openapi.yaml (TemplateView, StarterTemplateView,
// StarterTemplateDetailView, ValidateTemplateResponse). Beta: templates
// are unstable — keep in sync with the spec until they stabilize.

// GET /v1/templates returns SUMMARIES (metadata only, no body sources);
// GET /v1/templates/{id} returns the full view with the sources.
export type TemplateSummaryView = {
  id: string;
  name: string;
  alias?: string;
  subject: string;
  created_at: string;
  updated_at: string;
};

export type TemplateView = TemplateSummaryView & {
  body: string;
  html_body?: string;
};

export type StarterTemplateVariable = {
  name: string;
  required: boolean;
  raw: boolean;
  description: string;
  example: string;
};

export type StarterTemplateView = {
  alias: string;
  name: string;
  description: string;
  version: string;
  subject: string;
  variables: StarterTemplateVariable[];
};

// GET /v1/starter-templates/{alias} adds the verbatim body sources.
export type StarterTemplateDetail = StarterTemplateView & {
  body: string;
  html_body: string;
};

export type TemplatePartError = {
  part: string; // subject | body | html_body
  message: string;
};

export type RenderedTemplate = {
  subject: string;
  body: string;
  html_body?: string;
};

// suggested_data is NESTED: a dot-path variable like {{user.name}} emits
// {user: {name: "user.name_value"}}, matching how the renderer resolves
// dots. See _lib/testdata.ts for the flatten/nest display helpers.
export type SuggestedData = { [key: string]: string | SuggestedData };

export type ValidateTemplateResponse = {
  valid: boolean;
  errors: TemplatePartError[];
  rendered?: RenderedTemplate;
  suggested_data?: SuggestedData;
};

// Non-2xx /v1 responses use the ErrorEnvelope shape; branch on error.code
// (e.g. "alias_taken" on template create/update conflicts).
export type ErrorEnvelope = {
  error?: { code?: string; message?: string };
};

// Best-effort extraction of {code, message} from a failed Response body.
// Falls back to the raw text (matching how other pages surface errors).
export async function readErrorBody(
  res: Response,
): Promise<{ code: string; message: string }> {
  try {
    const text = await res.text();
    try {
      const body = JSON.parse(text) as ErrorEnvelope;
      return {
        code: body.error?.code ?? "",
        message: body.error?.message || text.trim() || `HTTP ${res.status}`,
      };
    } catch {
      return { code: "", message: text.trim() || `HTTP ${res.status}` };
    }
  } catch {
    return { code: "", message: `HTTP ${res.status}` };
  }
}
