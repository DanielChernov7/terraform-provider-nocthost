// Package client is a small HTTP client for the NoctHost API
// (https://nocthost.com/docs/api).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is the production API base URL from https://nocthost.com/openapi.json.
const DefaultEndpoint = "https://api.nocthost.com"

// Client talks to the NoctHost API.
type Client struct {
	Endpoint  string
	Token     string
	UserAgent string
	HTTP      *http.Client

	// MaxRetries is how many times a rate limited (429) or, for safe methods,
	// a gateway error (502/503/504) response is retried.
	MaxRetries int
	// RetryWait is the base backoff between retries. It doubles per attempt
	// unless the server sends Retry-After.
	RetryWait time.Duration
}

// New returns a client with sensible defaults.
func New(endpoint, token, userAgent string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{
		Endpoint:   strings.TrimRight(endpoint, "/"),
		Token:      token,
		UserAgent:  userAgent,
		HTTP:       &http.Client{Timeout: 60 * time.Second},
		MaxRetries: 5,
		RetryWait:  2 * time.Second,
	}
}

// ErrNoToken is returned when an authenticated endpoint is called without a token.
var ErrNoToken = errors.New("no API token configured: set `token` in the provider block or the NOCTHOST_TOKEN environment variable (create a key in the NoctHost dashboard under Profile, API keys)")

// APIError is a non-2xx response from the API.
type APIError struct {
	StatusCode int
	Message    string
	Details    json.RawMessage
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if len(e.Details) > 0 && string(e.Details) != "null" {
		return fmt.Sprintf("NoctHost API error (HTTP %d): %s: %s", e.StatusCode, msg, string(e.Details))
	}
	return fmt.Sprintf("NoctHost API error (HTTP %d): %s", e.StatusCode, msg)
}

// IsNotFound reports whether err is an HTTP 404 from the API.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// IsConflict reports whether err is an HTTP 409 from the API.
func IsConflict(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict
}

func (c *Client) do(ctx context.Context, method, path string, auth bool, body, out any) error {
	if auth && c.Token == "" {
		return ErrNoToken
	}

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
	}

	safe := method == http.MethodGet || method == http.MethodDelete
	wait := c.RetryWait
	for attempt := 0; ; attempt++ {
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.Endpoint+path, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}
		if auth {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			// A network error on a non-idempotent request (server create) is
			// never retried: the request may have been processed.
			if safe && attempt < c.MaxRetries && ctx.Err() == nil {
				if sleepErr := sleep(ctx, wait); sleepErr != nil {
					return sleepErr
				}
				wait *= 2
				continue
			}
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}

		retryable := resp.StatusCode == http.StatusTooManyRequests ||
			(safe && (resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout))
		if retryable && attempt < c.MaxRetries {
			d := wait
			if ra := retryAfter(resp.Header.Get("Retry-After")); ra > 0 {
				d = ra
			}
			if sleepErr := sleep(ctx, d); sleepErr != nil {
				return sleepErr
			}
			wait *= 2
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			apiErr := &APIError{StatusCode: resp.StatusCode}
			var e struct {
				Error   string          `json:"error"`
				Message string          `json:"message"`
				Details json.RawMessage `json:"details"`
			}
			if json.Unmarshal(data, &e) == nil {
				apiErr.Message = e.Error
				if apiErr.Message == "" {
					apiErr.Message = e.Message
				}
				apiErr.Details = e.Details
			} else if len(data) > 0 && len(data) < 500 {
				apiErr.Message = strings.TrimSpace(string(data))
			}
			return apiErr
		}

		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("decoding response from %s %s: %w", method, path, err)
			}
		}
		return nil
	}
}

func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Server is a server as returned by the API.
type Server struct {
	ID            string  `json:"id"`
	Label         string  `json:"label"`
	Status        string  `json:"status"`
	Provider      string  `json:"provider"`
	Region        string  `json:"region"`
	Plan          string  `json:"plan"`
	Image         string  `json:"image"`
	IPv4          *string `json:"ipv4"`
	HourlyPrice   string  `json:"hourlyPrice"`
	AccessType    string  `json:"accessType"`
	FailureReason *string `json:"failureReason"`
	CreatedAt     string  `json:"createdAt"`
}

// Server statuses.
const (
	StatusProvisioning = "PROVISIONING"
	StatusActive       = "ACTIVE"
	StatusDestroying   = "DESTROYING"
	StatusDestroyed    = "DESTROYED"
	StatusError        = "ERROR"
)

// CreateServerRequest is the body of POST /servers.
type CreateServerRequest struct {
	Label      string `json:"label"`
	PlanID     string `json:"planId"`
	RegionID   string `json:"regionId"`
	ProviderID string `json:"providerId,omitempty"`
	OSID       *int64 `json:"osId,omitempty"`
	AppSlug    string `json:"appSlug,omitempty"`
	Password   string `json:"password,omitempty"`
	SSHKey     string `json:"sshKey,omitempty"`
}

type serverEnvelope struct {
	Server *Server `json:"server"`
}

// CreateServer deploys one server. The API answers 202 with the server in
// PROVISIONING state; poll GetServer until it is ACTIVE.
func (c *Client) CreateServer(ctx context.Context, in CreateServerRequest) (*Server, error) {
	var out serverEnvelope
	if err := c.do(ctx, http.MethodPost, "/servers", true, in, &out); err != nil {
		return nil, err
	}
	if out.Server == nil || out.Server.ID == "" {
		return nil, errors.New("NoctHost API returned no server in the create response")
	}
	return out.Server, nil
}

// GetServer returns one server.
func (c *Client) GetServer(ctx context.Context, id string) (*Server, error) {
	var out serverEnvelope
	if err := c.do(ctx, http.MethodGet, "/servers/"+url.PathEscape(id), true, nil, &out); err != nil {
		return nil, err
	}
	if out.Server == nil {
		return nil, &APIError{StatusCode: http.StatusNotFound, Message: "Server not found"}
	}
	return out.Server, nil
}

// ListServers returns all non-destroyed servers.
func (c *Client) ListServers(ctx context.Context) ([]Server, error) {
	var out struct {
		Servers []Server `json:"servers"`
	}
	if err := c.do(ctx, http.MethodGet, "/servers", true, nil, &out); err != nil {
		return nil, err
	}
	return out.Servers, nil
}

// RenameServer changes the server label.
func (c *Client) RenameServer(ctx context.Context, id, label string) (*Server, error) {
	var out serverEnvelope
	if err := c.do(ctx, http.MethodPatch, "/servers/"+url.PathEscape(id), true, map[string]string{"label": label}, &out); err != nil {
		return nil, err
	}
	if out.Server == nil {
		return c.GetServer(ctx, id)
	}
	return out.Server, nil
}

// DeleteServer starts destroying a server. Billing stops the same hour.
func (c *Client) DeleteServer(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/servers/"+url.PathEscape(id), true, nil, nil)
}

// Provider is one cloud backend from GET /servers/providers.
type Provider struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
}

// ProvidersResponse is the part of GET /servers/providers this client uses.
type ProvidersResponse struct {
	Providers []Provider                   `json:"providers"`
	Outages   map[string]map[string]string `json:"outages"`
}

// GetProviders returns which backends take deploys now and which regions are
// temporarily out of capacity.
func (c *Client) GetProviders(ctx context.Context) (*ProvidersResponse, error) {
	var out ProvidersResponse
	if err := c.do(ctx, http.MethodGet, "/servers/providers", true, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Billing is GET /billing.
type Billing struct {
	Balance     string `json:"balance"`
	HourlySpend string `json:"hourlySpend"`
}

// GetBilling returns the prepaid balance and current hourly spend.
func (c *Client) GetBilling(ctx context.Context) (*Billing, error) {
	var out Billing
	if err := c.do(ctx, http.MethodGet, "/billing", true, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeployableOffer is one cloud and category in a city.
type DeployableOffer struct {
	CloudID        string  `json:"cloud_id"`
	Cloud          string  `json:"cloud"`
	Category       string  `json:"category"`
	CategoryName   string  `json:"category_name"`
	Sizes          int64   `json:"sizes"`
	FromHourlyUSD  float64 `json:"from_hourly_usd"`
	FromMonthlyUSD float64 `json:"from_monthly_usd"`
}

// DeployableLocation is one city with what can be deployed there.
type DeployableLocation struct {
	RegionID  string            `json:"region_id"`
	City      string            `json:"city"`
	Continent string            `json:"continent"`
	Offers    []DeployableOffer `json:"offers"`
}

// DeployableCloud is one cloud backend in the public catalog.
type DeployableCloud struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Cities         int64    `json:"cities"`
	Sizes          int64    `json:"sizes"`
	Categories     []string `json:"categories"`
	FromHourlyUSD  float64  `json:"from_hourly_usd"`
	FromMonthlyUSD float64  `json:"from_monthly_usd"`
}

// Deployable is the public GET /public/v1/deployable document.
type Deployable struct {
	GeneratedAt string               `json:"generated_at"`
	Clouds      []DeployableCloud    `json:"clouds"`
	Locations   []DeployableLocation `json:"locations"`
}

// GetDeployable returns what can be deployed right now. No token needed.
func (c *Client) GetDeployable(ctx context.Context) (*Deployable, error) {
	var out Deployable
	if err := c.do(ctx, http.MethodGet, "/public/v1/deployable", false, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GPURegion is a region where a GPU size is sold, with its price there.
type GPURegion struct {
	ID               string  `json:"id"`
	City             string  `json:"city"`
	Continent        string  `json:"continent"`
	PricePerHourUSD  float64 `json:"price_per_hour_usd"`
	PricePerMonthUSD float64 `json:"price_per_month_usd"`
}

// GPUOffer is one GPU size from GET /public/v1/gpus.
type GPUOffer struct {
	ID               string      `json:"id"`
	Cloud            string      `json:"cloud"`
	GPUModel         string      `json:"gpu_model"`
	GPUCount         *int64      `json:"gpu_count"`
	Fractional       bool        `json:"fractional"`
	VRAMGBPerGPU     *float64    `json:"vram_gb_per_gpu"`
	VRAMGBTotal      float64     `json:"vram_gb_total"`
	VCPU             int64       `json:"vcpu"`
	RAMGB            float64     `json:"ram_gb"`
	DiskGB           float64     `json:"disk_gb"`
	PricePerHourUSD  float64     `json:"price_per_hour_usd"`
	PricePerMonthUSD float64     `json:"price_per_month_usd"`
	Regions          []GPURegion `json:"regions"`
}

// GPUFilter narrows GET /public/v1/gpus. Zero values are not sent.
type GPUFilter struct {
	GPUModel     string
	MinVRAMGB    float64
	Continent    string
	MaxHourlyUSD float64
	Cloud        string
}

// GetGPUs returns GPU sizes and hourly prices. No token needed.
func (c *Client) GetGPUs(ctx context.Context, f GPUFilter) ([]GPUOffer, error) {
	q := url.Values{}
	if f.GPUModel != "" {
		q.Set("gpu_model", f.GPUModel)
	}
	if f.MinVRAMGB > 0 {
		q.Set("min_vram_gb", strconv.FormatFloat(f.MinVRAMGB, 'f', -1, 64))
	}
	if f.Continent != "" {
		q.Set("continent", f.Continent)
	}
	if f.MaxHourlyUSD > 0 {
		q.Set("max_hourly_usd", strconv.FormatFloat(f.MaxHourlyUSD, 'f', -1, 64))
	}
	if f.Cloud != "" {
		q.Set("cloud", f.Cloud)
	}
	path := "/public/v1/gpus"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Offers []GPUOffer `json:"offers"`
	}
	if err := c.do(ctx, http.MethodGet, path, false, nil, &out); err != nil {
		return nil, err
	}
	return out.Offers, nil
}
