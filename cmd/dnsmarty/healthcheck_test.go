package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbe(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "db", http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer slow.Close()

	if err := probe(context.Background(), ok.URL, time.Second); err != nil {
		t.Fatalf("200: %v", err)
	}
	if err := probe(context.Background(), bad.URL, time.Second); err == nil {
		t.Fatal("503 counted as healthy")
	}
	if err := probe(context.Background(), slow.URL, 100*time.Millisecond); err == nil {
		t.Fatal("timeout did not fire")
	}
	if err := probe(context.Background(), "http://127.0.0.1:1/healthz", time.Second); err == nil {
		t.Fatal("closed port counted as healthy")
	}
}
