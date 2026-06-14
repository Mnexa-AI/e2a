package httpapi

import (
	"context"
	"net/http"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

// Authenticator resolves the calling principal from the raw request. It is
// injected so this package reuses the *single* auth path that already lives
// in the agent layer (API key, OAuth bearer, session cookie) instead of
// forking a second one — there is exactly one place credentials are checked.
type Authenticator func(r *http.Request) (*identity.User, error)

// AgentLister returns the agents owned by a user. Injected as a narrow
// function so the foundation slice doesn't depend on the whole store; the
// remaining ports will widen this into a resource-scoped store interface.
type AgentLister func(ctx context.Context, userID string) ([]identity.AgentIdentity, error)

// AgentGetter loads a single agent by its full email address (the
// identifier). Ownership is checked by the caller against the resolved
// agent's UserID.
type AgentGetter func(ctx context.Context, address string) (*identity.AgentIdentity, error)

// Deps are the collaborators the v1 layer needs. Everything is injected so
// the package has no hidden globals and is straightforward to test.
type Deps struct {
	Authenticator Authenticator
	ListAgents    AgentLister
	GetAgent      AgentGetter

	// Deployment info surfaced by GET /v1/info (unchanged shape from the
	// legacy /api/v1/info while we are in the consistency-only slice).
	SharedDomain string
	PublicURL    string

	// Legacy is the existing gorilla/mux handler. The chi root falls back
	// to it for every route not yet ported onto Huma (the strangler), so
	// the service stays fully functional through the multi-sub-slice port.
	Legacy http.Handler
}

// Server is the v1 HTTP surface: a chi root router with the Huma API mounted
// on it and the legacy handler wired as the fallback.
type Server struct {
	Router chi.Router
	API    huma.API
	deps   Deps
}

// New builds the v1 server. It installs the e2a error envelope globally,
// stands up the Huma API on a chi router under the `/v1` documentation
// paths, registers the ported operations, and points chi's not-found/
// method-not-allowed handlers at the legacy surface.
func New(deps Deps) *Server {
	installErrorEnvelope()

	root := chi.NewRouter()
	root.Use(requestID)
	root.Use(securityHeaders)
	root.Use(withRawRequest)

	config := huma.DefaultConfig("e2a API", "1.0.0")
	// Serve the spec and human docs under the versioned prefix so they sit
	// beside the operations (api-v1-redesign §1: everything lives under the
	// api host; here, under /v1).
	config.OpenAPIPath = "/v1/openapi"
	config.DocsPath = "/v1/docs"
	config.SchemasPath = "/v1/schemas"
	// Drop Huma's default schema-link transformer: it injects a `$schema`
	// field and Link header into response bodies, which would change the
	// clean contract shape this redesign is standardizing. Keep only our
	// request-id stamper.
	config.CreateHooks = nil
	config.Transformers = []huma.Transformer{stampRequestID}
	config.Info.Description = "e2a — authenticated email gateway for AI agents. v1 contract."
	// One auth scheme across the surface: a Bearer credential that is
	// either an API key or an OAuth 2.1 access token (api-v1-redesign §5).
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer": {
			Type:        "http",
			Scheme:      "bearer",
			Description: "API key (e2a_…) or OAuth 2.1 access token, sent as `Authorization: Bearer <token>`.",
		},
	}

	api := humachi.New(root, config)

	s := &Server{Router: root, API: api, deps: deps}
	s.registerOperations()

	if deps.Legacy != nil {
		root.NotFound(deps.Legacy.ServeHTTP)
		root.MethodNotAllowed(deps.Legacy.ServeHTTP)
	}
	return s
}

// ServeHTTP makes Server a drop-in http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Router.ServeHTTP(w, r)
}

// OpenAPIYAML renders the generated spec as YAML. Used by the codegen step
// and the drift test — the spec is emitted from the live handlers, never
// hand-authored.
func (s *Server) OpenAPIYAML() ([]byte, error) {
	return s.API.OpenAPI().YAML()
}

// registerOperations wires every ported Huma operation. As resources move
// off the legacy mux they are added here and removed from the legacy
// RegisterRoutes in the same commit.
func (s *Server) registerOperations() {
	s.registerInfo()
	s.registerAgents()
}

// reqCtxKey carries the raw *http.Request through to Huma handlers so they
// can reuse the injected Authenticator (which reads headers + cookies).
type reqCtxKey struct{}

// withRawRequest stashes the request so Huma handlers can recover it for
// the auth path. Storing the request in its own derived context is the
// standard bridge; only headers/cookies are read downstream, so the
// pre-derivation request is equivalent for authentication.
func withRawRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), reqCtxKey{}, r)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestFromContext recovers the raw request stashed by withRawRequest.
func RequestFromContext(ctx context.Context) *http.Request {
	if r, ok := ctx.Value(reqCtxKey{}).(*http.Request); ok {
		return r
	}
	return nil
}

// requireUser authenticates the caller or returns a 401 envelope carrying
// the machine-branchable "unauthorized" code.
func (s *Server) requireUser(ctx context.Context) (*identity.User, error) {
	r := RequestFromContext(ctx)
	if r == nil || s.deps.Authenticator == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "authentication unavailable")
	}
	user, err := s.deps.Authenticator(r)
	if err != nil {
		return nil, NewError(http.StatusUnauthorized, "unauthorized", "authentication required")
	}
	return user, nil
}
