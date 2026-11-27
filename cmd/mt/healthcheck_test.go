package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheckURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "http://127.0.0.1:8080/healthz"},
		{":8080", "http://127.0.0.1:8080/healthz"},
		{"0.0.0.0:9090", "http://127.0.0.1:9090/healthz"},
		{"[::]:8081", "http://127.0.0.1:8081/healthz"},
		{"127.0.0.1:8092", "http://127.0.0.1:8092/healthz"},
	}
	for _, tc := range cases {
		if got := healthcheckURL(tc.in); got != tc.want {
			t.Errorf("healthcheckURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCmdHealthcheck(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	if err := cmdHealthcheck([]string{"-url", okSrv.URL}); err != nil {
		t.Fatalf("expected nil error for 200 OK, got %v", err)
	}

	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failSrv.Close()
	if err := cmdHealthcheck([]string{"-url", failSrv.URL}); err == nil {
		t.Fatal("expected error for 503 status, got nil")
	}
}
