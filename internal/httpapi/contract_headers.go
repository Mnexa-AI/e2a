package httpapi

import "github.com/danielgtaylor/huma/v2"

const (
	headerXRequestID      = "XRequestID"
	headerRetryAfter      = "RetryAfter"
	headerRateLimitLimit  = "RateLimitLimit"
	headerRateLimitRemain = "RateLimitRemaining"
	headerRateLimitReset  = "RateLimitReset"
)

func headerRef(name string) *huma.Param {
	return &huma.Param{Ref: "#/components/headers/" + name}
}

// applyResponseHeaderContract documents headers emitted centrally by the
// request-id and rate-limit middleware. It enriches existing response objects;
// operation registration remains responsible for declaring possible statuses.
func (s *Server) applyResponseHeaderContract() {
	oapi := s.API.OpenAPI()
	minimumOne := float64(1)
	if oapi.Components.Headers == nil {
		oapi.Components.Headers = map[string]*huma.Header{}
	}
	oapi.Components.Headers[headerXRequestID] = &huma.Header{
		Description: "Always-present request correlation id. Error responses echo the same value in error.request_id.",
		Schema:      &huma.Schema{Type: huma.TypeString},
	}
	oapi.Components.Headers[headerRetryAfter] = &huma.Header{
		Description: "Positive integer seconds to wait before retrying a transient 429 or 503 response.",
		Schema:      &huma.Schema{Type: huma.TypeInteger, Minimum: &minimumOne},
	}
	oapi.Components.Headers[headerRateLimitLimit] = &huma.Header{
		Description: "Request quota for the current limiter window.",
		Schema:      &huma.Schema{Type: huma.TypeInteger},
	}
	oapi.Components.Headers[headerRateLimitRemain] = &huma.Header{
		Description: "Requests remaining in the current limiter window.",
		Schema:      &huma.Schema{Type: huma.TypeInteger},
	}
	oapi.Components.Headers[headerRateLimitReset] = &huma.Header{
		Description: "Seconds until the current limiter window resets.",
		Schema:      &huma.Schema{Type: huma.TypeInteger},
	}
	forEachOperation(oapi, func(op *huma.Operation) {
		for status, response := range op.Responses {
			if response.Headers == nil {
				response.Headers = map[string]*huma.Param{}
			}
			response.Headers["X-Request-Id"] = headerRef(headerXRequestID)
			if status == "429" || status == "503" {
				response.Headers["Retry-After"] = headerRef(headerRetryAfter)
			}
			if pollLimitedOps[op.OperationID] || op.OperationID == "createAgent" {
				response.Headers["RateLimit-Limit"] = headerRef(headerRateLimitLimit)
				response.Headers["RateLimit-Remaining"] = headerRef(headerRateLimitRemain)
				response.Headers["RateLimit-Reset"] = headerRef(headerRateLimitReset)
			}
		}
	})
}
