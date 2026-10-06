// SPDX-License-Identifier: AGPL-3.0-only

package enterprise

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientNegotiatesMajorAndFailsOpenForCore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"product":"netriun-nexus-enterprise","service_version":"1.0.0","api_majors":[1],"capabilities":[]}`))
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = (*Client)(nil).Info(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("optional Enterprise service must report unavailable, got %v", err)
	}
}

func TestClientRejectsIncompatibleMajor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"product":"enterprise","service_version":"2","api_majors":[2],"capabilities":[]}`))
	}))
	defer server.Close()
	client, _ := New(server.URL, server.Client())
	if _, err := client.Info(context.Background()); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected incompatible API, got %v", err)
	}
}
