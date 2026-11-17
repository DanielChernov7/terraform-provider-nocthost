package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "nh_testtokentesttokentesttoken00"

// mockServer is an in-memory stand-in for the NoctHost API. It follows the
// shapes of https://nocthost.com/openapi.json and apps/api routes: create
// answers 202 with PROVISIONING, GET flips to ACTIVE after a few polls,
// DELETE answers 202 and the server goes DESTROYING then DESTROYED.
type mockServer struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	servers map[string]*mockSrv
	nextID  int
	creates []map[string]any
	renames int

	// pollsToActive is how many GETs a new server stays PROVISIONING.
	pollsToActive int
	// pollsToGone is how many GETs a deleted server stays DESTROYING.
	pollsToGone int
	// failDeploy makes new servers end in ERROR instead of ACTIVE.
	failDeploy bool
	// neverActive keeps new servers PROVISIONING forever.
	neverActive bool
}

type mockSrv struct {
	ID          string  `json:"id"`
	Label       string  `json:"label"`
	Status      string  `json:"status"`
	Provider    string  `json:"provider"`
	Region      string  `json:"region"`
	Plan        string  `json:"plan"`
	Image       string  `json:"image"`
	IPv4        *string `json:"ipv4"`
	HourlyPrice string  `json:"hourlyPrice"`
	AccessType  string  `json:"accessType"`
	Failure     *string `json:"failureReason"`
	CreatedAt   string  `json:"createdAt"`

	polls int
}

func newMockServer(t *testing.T) *mockServer {
	t.Helper()
	m := &mockServer{t: t, servers: map[string]*mockSrv{}, pollsToActive: 2, pollsToGone: 2}
	m.srv = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.srv.Close)
	old := pollInterval
	pollInterval = 10 * time.Millisecond
	t.Cleanup(func() { pollInterval = old })
	return m
}

func (m *mockServer) URL() string { return m.srv.URL }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *mockServer) handle(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p := r.URL.Path
	if strings.HasPrefix(p, "/public/v1/") {
		if r.Header.Get("Authorization") != "" {
			m.t.Errorf("public endpoint %s got an Authorization header", p)
		}
		switch p {
		case "/public/v1/deployable":
			_, _ = w.Write([]byte(deployableFixture))
		case "/public/v1/gpus":
			m.gpus(w, r)
		default:
			writeJSON(w, 404, map[string]string{"error": "Not found"})
		}
		return
	}

	if r.Header.Get("Authorization") != "Bearer "+testToken {
		writeJSON(w, 401, map[string]string{"error": "Unauthorized"})
		return
	}

	switch {
	case p == "/billing" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]string{"balance": "9.96", "hourlySpend": "0.012"})
	case p == "/servers/providers" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{
			"providers": []map[string]any{
				{"id": "vultr", "label": "Vultr", "available": true},
				{"id": "hetzner", "label": "Hetzner", "available": true},
			},
			"outages": map[string]map[string]string{
				"hetzner": {"hel": "Hetzner Helsinki is paused for new servers", "fra": "Hetzner Frankfurt is paused for new servers"},
			},
		})
	case p == "/servers" && r.Method == http.MethodGet:
		list := []*mockSrv{}
		for _, s := range m.servers {
			if s.Status != "DESTROYED" {
				list = append(list, s)
			}
		}
		writeJSON(w, 200, map[string]any{"servers": list, "reipEnabled": false})
	case p == "/servers" && r.Method == http.MethodPost:
		m.create(w, r)
	case strings.HasPrefix(p, "/servers/"):
		id := strings.TrimPrefix(p, "/servers/")
		s, ok := m.servers[id]
		if !ok {
			writeJSON(w, 404, map[string]string{"error": "Server not found"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			m.advance(s)
			writeJSON(w, 200, map[string]any{"server": s, "reipEnabled": false})
		case http.MethodPatch:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["label"] == "" {
				writeJSON(w, 400, map[string]string{"error": "Label required"})
				return
			}
			s.Label = body["label"]
			m.renames++
			writeJSON(w, 200, map[string]any{"server": s})
		case http.MethodDelete:
			if s.Status == "DESTROYED" || s.Status == "DESTROYING" {
				writeJSON(w, 409, map[string]string{"error": "Already being destroyed"})
				return
			}
			s.Status = "DESTROYING"
			s.polls = 0
			writeJSON(w, 202, map[string]bool{"ok": true})
		default:
			writeJSON(w, 405, map[string]string{"error": "Method not allowed"})
		}
	default:
		writeJSON(w, 404, map[string]string{"error": "Not found"})
	}
}

func (m *mockServer) create(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "Validation failed"})
		return
	}
	for _, k := range []string{"label", "planId", "regionId"} {
		if s, _ := body[k].(string); s == "" {
			writeJSON(w, 400, map[string]any{"error": "Validation failed", "details": map[string]any{"fieldErrors": map[string]any{k: []string{"Required"}}}})
			return
		}
	}
	if _, ok := body["osId"]; ok {
		if _, ok2 := body["appSlug"]; ok2 {
			writeJSON(w, 400, map[string]string{"error": "Choose either an OS or an app, not both"})
			return
		}
	}
	if body["planId"] == "unknown-plan" {
		writeJSON(w, 400, map[string]string{"error": "Unknown plan or region"})
		return
	}
	m.creates = append(m.creates, body)
	m.nextID++
	provider, _ := body["providerId"].(string)
	if provider == "" {
		provider = "vultr"
	}
	s := &mockSrv{
		ID:          fmt.Sprintf("cmsrv%04d", m.nextID),
		Label:       body["label"].(string),
		Status:      "PROVISIONING",
		Provider:    provider,
		Region:      body["regionId"].(string),
		Plan:        body["planId"].(string),
		Image:       "Ubuntu 22.04 x64",
		HourlyPrice: "0.0137",
		AccessType:  "ssh",
		CreatedAt:   "2026-11-17T12:00:00.000Z",
	}
	m.servers[s.ID] = s
	writeJSON(w, 202, map[string]any{"server": s})
}

// advance moves a server along its lifecycle on each GET.
func (m *mockServer) advance(s *mockSrv) {
	s.polls++
	switch s.Status {
	case "PROVISIONING":
		if m.neverActive {
			return
		}
		if s.polls > m.pollsToActive {
			if m.failDeploy {
				s.Status = "ERROR"
				reason := "provider_error"
				s.Failure = &reason
				return
			}
			s.Status = "ACTIVE"
			ip := fmt.Sprintf("203.0.113.%d", 10+len(m.servers))
			s.IPv4 = &ip
		}
	case "DESTROYING":
		if s.polls > m.pollsToGone {
			s.Status = "DESTROYED"
		}
	}
}

// destroyOutOfBand simulates a server deleted from the dashboard.
func (m *mockServer) destroyOutOfBand() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.servers {
		s.Status = "DESTROYED"
	}
}

func (m *mockServer) createCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.creates)
}

func (m *mockServer) lastCreate() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.creates) == 0 {
		return nil
	}
	return m.creates[len(m.creates)-1]
}

func (m *mockServer) liveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.servers {
		if s.Status != "DESTROYED" {
			n++
		}
	}
	return n
}

func (m *mockServer) gpus(w http.ResponseWriter, r *http.Request) {
	var doc struct {
		Offers []map[string]any `json:"offers"`
	}
	_ = json.Unmarshal([]byte(gpusFixture), &doc)
	q := r.URL.Query()
	out := []map[string]any{}
	for _, o := range doc.Offers {
		if c := q.Get("cloud"); c != "" && !strings.EqualFold(c, o["cloud"].(string)) {
			continue
		}
		if g := q.Get("gpu_model"); g != "" && !strings.Contains(strings.ToLower(o["gpu_model"].(string)), strings.ToLower(g)) {
			continue
		}
		out = append(out, o)
	}
	writeJSON(w, 200, map[string]any{"offer_count": len(out), "offers": out})
}

// Trimmed copies of the live public documents (17.11.2026).
const deployableFixture = `{
 "provider": "NoctHost",
 "generated_at": "2026-11-17T12:02:52.861Z",
 "source": "live",
 "currency": "USD",
 "clouds": [
  {"id": "digitalocean", "name": "DigitalOcean", "cities": 9, "sizes": 50, "categories": ["shared","dedicated"], "from_hourly_usd": 0.013699, "from_monthly_usd": 10},
  {"id": "hetzner", "name": "Hetzner", "cities": 6, "sizes": 19, "categories": ["shared","dedicated"], "from_hourly_usd": 0.013699, "from_monthly_usd": 10}
 ],
 "locations": [
  {"region_id": "ams", "city": "Amsterdam", "continent": "Europe", "offers": [
    {"cloud_id": "digitalocean", "cloud": "DigitalOcean", "category": "shared", "category_name": "Shared VPS", "sizes": 28, "from_hourly_usd": 0.013699, "from_monthly_usd": 10},
    {"cloud_id": "linode", "cloud": "Linode", "category": "dedicated", "category_name": "Dedicated CPU", "sizes": 22, "from_hourly_usd": 0.083562, "from_monthly_usd": 61}
  ]},
  {"region_id": "nbg", "city": "Nuremberg", "continent": "Europe", "offers": [
    {"cloud_id": "hetzner", "cloud": "Hetzner", "category": "shared", "category_name": "Shared VPS", "sizes": 8, "from_hourly_usd": 0.013699, "from_monthly_usd": 10}
  ]},
  {"region_id": "ewr", "city": "New Jersey", "continent": "North America", "offers": [
    {"cloud_id": "vultr", "cloud": "Vultr", "category": "shared", "category_name": "Shared VPS", "sizes": 30, "from_hourly_usd": 0.013699, "from_monthly_usd": 10}
  ]}
 ]
}`

const gpusFixture = `{
 "offers": [
  {"id": "vcg-a16-6c-60g-16vram", "cloud": "Vultr", "gpu_model": "A16", "gpu_count": null, "fractional": false, "vram_gb_per_gpu": null, "vram_gb_total": 16, "vcpu": 6, "ram_gb": 60, "disk_gb": 350, "price_per_hour_usd": 0.6575, "price_per_month_usd": 480,
   "regions": [{"id": "fra", "city": "Frankfurt", "continent": "Europe", "price_per_hour_usd": 0.6575, "price_per_month_usd": 480}]},
  {"id": "g2-gpu-rtx4000a1-s", "cloud": "Linode", "gpu_model": "RTX 4000 Ada", "gpu_count": 1, "fractional": false, "vram_gb_per_gpu": 20, "vram_gb_total": 20, "vcpu": 4, "ram_gb": 16, "disk_gb": 500, "price_per_hour_usd": 0.52, "price_per_month_usd": 350,
   "regions": [{"id": "fra", "city": "Frankfurt", "continent": "Europe", "price_per_hour_usd": 0.52, "price_per_month_usd": 350}, {"id": "ord", "city": "Chicago", "continent": "North America", "price_per_hour_usd": 0.52, "price_per_month_usd": 350}]},
  {"id": "l40s-90", "cloud": "OVHcloud", "gpu_model": "L40S", "gpu_count": 1, "fractional": false, "vram_gb_per_gpu": 48, "vram_gb_total": 48, "vcpu": 15, "ram_gb": 90, "disk_gb": 400, "price_per_hour_usd": 1.9, "price_per_month_usd": 1300,
   "regions": [{"id": "par", "city": "Paris", "continent": "Europe", "price_per_hour_usd": 1.9, "price_per_month_usd": 1300}]}
 ]
}`
