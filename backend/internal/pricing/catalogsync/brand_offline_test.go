package catalogsync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/brand"
)

func TestFetchMakesNoRequestWhenPricingSyncIsCompiledOut(t *testing.T) {
	old := brand.PricingCatalogSyncEnabled
	brand.PricingCatalogSyncEnabled = "false"
	t.Cleanup(func() { brand.PricingCatalogSyncEnabled = old })

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	result, err := NewFetcher(srv.Client(), srv.URL).Fetch(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.NotModified || result.Catalog != nil {
		t.Fatalf("offline fetch must answer NotModified with no catalog, got %+v", result)
	}
	if hits.Load() != 0 {
		t.Fatalf("%d requests reached the server; the price catalog must not be fetched when compiled out", hits.Load())
	}
}

func TestFetchStillRequestsWhenPricingSyncIsOn(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := NewFetcher(srv.Client(), srv.URL).Fetch(context.Background(), "", false); err == nil {
		t.Fatal("expected the 500 to surface as an error")
	}
	if hits.Load() != 1 {
		t.Fatal("legacy builds must keep fetching the catalog")
	}
}
