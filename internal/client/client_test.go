package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(url, token string) *Client {
	c := New(url, token, "test")
	c.RetryWait = time.Millisecond
	return c
}

func TestRetriesOn429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"balance":"1.50","hourlySpend":"0"}`))
	}))
	defer srv.Close()

	b, err := testClient(srv.URL, "nh_x").GetBilling(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.Balance != "1.50" || calls != 3 {
		t.Fatalf("balance=%s calls=%d", b.Balance, calls)
	}
}

func TestCreateNotRetriedOnGatewayError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := testClient(srv.URL, "nh_x").CreateServer(context.Background(), CreateServerRequest{Label: "a", PlanID: "p", RegionID: "r"})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("a server create must never be retried after a 5xx (could deploy twice), got %d calls", calls)
	}
}

func TestGetRetriedOnGatewayError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"servers":[]}`))
	}))
	defer srv.Close()

	if _, err := testClient(srv.URL, "nh_x").ListServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestErrorParsing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer nh_x" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Server not found"}`))
	}))
	defer srv.Close()

	_, err := testClient(srv.URL, "nh_x").GetServer(context.Background(), "abc")
	if !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
	if !strings.Contains(err.Error(), "Server not found") {
		t.Fatalf("message lost: %v", err)
	}
}

func TestNoTokenNoRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request sent without a token")
	}))
	defer srv.Close()

	_, err := testClient(srv.URL, "").ListServers(context.Background())
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("expected ErrNoToken, got %v", err)
	}
}

func TestPublicEndpointsNeedNoToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("public endpoint got Authorization")
		}
		if r.URL.Path == "/public/v1/gpus" {
			if r.URL.Query().Get("gpu_model") != "L40S" || r.URL.Query().Get("min_vram_gb") != "48" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"offers":[{"id":"l40s-90","cloud":"OVHcloud","gpu_model":"L40S","gpu_count":1,"vram_gb_total":48,"regions":[]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"generated_at":"x","locations":[{"region_id":"ams","city":"Amsterdam","continent":"Europe","offers":[]}]}`))
	}))
	defer srv.Close()

	c := testClient(srv.URL, "")
	d, err := c.GetDeployable(context.Background())
	if err != nil || len(d.Locations) != 1 {
		t.Fatalf("deployable: %v %+v", err, d)
	}
	g, err := c.GetGPUs(context.Background(), GPUFilter{GPUModel: "L40S", MinVRAMGB: 48})
	if err != nil || len(g) != 1 || *g[0].GPUCount != 1 {
		t.Fatalf("gpus: %v %+v", err, g)
	}
}

func TestRetryAfterHeader(t *testing.T) {
	if d := retryAfter("3"); d != 3*time.Second {
		t.Fatalf("got %v", d)
	}
	if d := retryAfter(""); d != 0 {
		t.Fatalf("got %v", d)
	}
}
