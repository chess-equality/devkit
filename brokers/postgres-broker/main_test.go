package main

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
)

func TestStripVersionPrefix(t *testing.T) {
	cases := map[string]string{
		"/_ping":                    "/_ping",
		"/v1.41/_ping":              "/_ping",
		"/v999.1/containers/create": "/containers/create",
	}

	for input, expected := range cases {
		if got := stripVersionPrefix(input); got != expected {
			t.Fatalf("expected %s got %s", expected, got)
		}
	}
}

func TestAuthorizeContainerCreate_AllowsWhitelistedImage(t *testing.T) {
	rc := &requestContext{
		policy: &policy{allowedImage: "postgres", allowedTag: "latest"},
	}

	body := []byte(`{"Image":"postgres:latest","HostConfig":{}}`)
	req, err := http.NewRequest(http.MethodPost, "http://unix/containers/create", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rc.authorizeContainerCreate(req); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
}

func TestAuthorizeContainerCreate_BlocksOtherImages(t *testing.T) {
	rc := &requestContext{
		policy: &policy{allowedImage: "postgres", allowedTag: "latest"},
	}

	body := []byte(`{"Image":"redis:latest","HostConfig":{}}`)
	req, err := http.NewRequest(http.MethodPost, "http://unix/containers/create", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rc.authorizeContainerCreate(req); err == nil {
		t.Fatal("expected error but got nil")
	}
}

func TestAuthorizeImageCreate_AllowsWhitelistedPull(t *testing.T) {
	rc := &requestContext{policy: &policy{allowedImage: "postgres", allowedTag: "latest", allowPull: true}}

	req, _ := http.NewRequest(http.MethodPost, "http://unix/images/create", nil)
	req.URL = &url.URL{RawQuery: "fromImage=postgres&tag=latest"}

	if err := rc.authorizeImageCreate(req); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
}

func TestAuthorizeImageCreate_BlocksWhenDisabled(t *testing.T) {
	rc := &requestContext{policy: &policy{allowedImage: "postgres", allowedTag: "latest", allowPull: false}}

	req, _ := http.NewRequest(http.MethodPost, "http://unix/images/create", nil)
	req.URL = &url.URL{RawQuery: "fromImage=postgres&tag=latest"}

	if err := rc.authorizeImageCreate(req); err == nil {
		t.Fatal("expected block")
	}
}

func TestBuildClient_Unix(t *testing.T) {
	client, target, err := buildClient("unix:///var/run/docker.sock")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil || target.String() != "http://docker" {
		t.Fatalf("unexpected target: %v", target)
	}
}

func TestBuildClient_TCP(t *testing.T) {
	client, target, err := buildClient("tcp://docker:2375")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil || target.String() != "http://docker:2375" {
		t.Fatalf("unexpected target: %v", target)
	}
}
