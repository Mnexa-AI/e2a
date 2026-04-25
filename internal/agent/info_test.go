package agent_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/usage"
	"github.com/gorilla/mux"
)

// TestHandleInfo doesn't touch the database — /info only reads two fields
// off the API struct — so we construct an API with nil store/sender and
// hit the route directly. Keeps this test in the unit tier and runnable
// without docker compose.
func TestHandleInfo(t *testing.T) {
	cases := []struct {
		name                    string
		sharedDomain            string
		publicURL               string
		wantSharedDomain        string
		wantSlugEnabled         bool
		wantPublicURLPresent    bool
	}{
		{
			name:                 "fully configured deployment",
			sharedDomain:         "agents.example.com",
			publicURL:            "https://e2a.example.com",
			wantSharedDomain:     "agents.example.com",
			wantSlugEnabled:      true,
			wantPublicURLPresent: true,
		},
		{
			name:                 "no shared domain disables slug registration",
			sharedDomain:         "",
			publicURL:            "https://self-host.example.com",
			wantSharedDomain:     "",
			wantSlugEnabled:      false,
			wantPublicURLPresent: true,
		},
		{
			name:                 "minimal deployment with neither value",
			sharedDomain:         "",
			publicURL:            "",
			wantSharedDomain:     "",
			wantSlugEnabled:      false,
			wantPublicURLPresent: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := agent.NewAPI(nil, nil, nil, nil, usage.NewNoopUsageTracker(),
				"smtp.example.com", "send.example.com", tc.sharedDomain, tc.publicURL, false)
			router := mux.NewRouter()
			api.RegisterRoutes(router)
			server := httptest.NewServer(router)
			t.Cleanup(server.Close)

			resp, err := http.Get(server.URL + "/api/v1/info")
			if err != nil {
				t.Fatalf("GET /api/v1/info: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}

			var got struct {
				SharedDomain            string `json:"shared_domain"`
				SlugRegistrationEnabled bool   `json:"slug_registration_enabled"`
				PublicURL               string `json:"public_url,omitempty"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.SharedDomain != tc.wantSharedDomain {
				t.Errorf("shared_domain = %q, want %q", got.SharedDomain, tc.wantSharedDomain)
			}
			if got.SlugRegistrationEnabled != tc.wantSlugEnabled {
				t.Errorf("slug_registration_enabled = %v, want %v", got.SlugRegistrationEnabled, tc.wantSlugEnabled)
			}
			if (got.PublicURL != "") != tc.wantPublicURLPresent {
				t.Errorf("public_url presence = %v (got %q), want present=%v", got.PublicURL != "", got.PublicURL, tc.wantPublicURLPresent)
			}
		})
	}
}

// TestHandleInfoUnauthenticated verifies the endpoint doesn't require an
// API key. CLIs hit this *before* they have credentials so authentication
// would defeat the point.
func TestHandleInfoUnauthenticated(t *testing.T) {
	api := agent.NewAPI(nil, nil, nil, nil, usage.NewNoopUsageTracker(),
		"smtp.example.com", "send.example.com", "agents.example.com", "https://e2a.example.com", false)
	router := mux.NewRouter()
	api.RegisterRoutes(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	// No Authorization header.
	resp, err := http.Get(server.URL + "/api/v1/info")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (endpoint must be unauthenticated)", resp.StatusCode)
	}
}
