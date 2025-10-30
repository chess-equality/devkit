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
	rc := &requestContext{policy: mustPolicy(t, []string{"postgres:latest"}, true)}

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
	rc := &requestContext{policy: mustPolicy(t, []string{"postgres:latest"}, true)}

	body := []byte(`{"Image":"redis:latest","HostConfig":{}}`)
	req, err := http.NewRequest(http.MethodPost, "http://unix/containers/create", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rc.authorizeContainerCreate(req); err == nil {
		t.Fatal("expected error but got nil")
	}
}

func TestAuthorizeContainerCreate_AllowsMinioPorts(t *testing.T) {
	rc := &requestContext{policy: mustPolicy(t, []string{"minio/minio:latest"}, true)}
	body := []byte(`{"Image":"minio/minio:latest","HostConfig":{"PortBindings":{"9000/tcp":[{"HostPort":""}],"9001/tcp":[{"HostPort":""}]}}}`)
	req, err := http.NewRequest(http.MethodPost, "http://unix/containers/create", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rc.authorizeContainerCreate(req); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
}

func TestAuthorizeContainerCreate_BlocksMinioHostPortOverride(t *testing.T) {
	rc := &requestContext{policy: mustPolicy(t, []string{"minio/minio:latest"}, true)}
	body := []byte(`{"Image":"minio/minio:latest","HostConfig":{"PortBindings":{"9000/tcp":[{"HostPort":"1234"}],"9001/tcp":[{"HostPort":""}]}}}`)
	req, err := http.NewRequest(http.MethodPost, "http://unix/containers/create", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := rc.authorizeContainerCreate(req); err == nil {
		t.Fatal("expected block when host port is forced")
	}
}

func TestAuthorizeImageCreate_AllowsWhitelistedPull(t *testing.T) {
	rc := &requestContext{policy: mustPolicy(t, []string{"postgres:latest"}, true)}

	req, _ := http.NewRequest(http.MethodPost, "http://unix/images/create", nil)
	req.URL = &url.URL{RawQuery: "fromImage=postgres&tag=latest"}

	if err := rc.authorizeImageCreate(req); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
}

func TestAuthorizeImageCreate_BlocksWhenDisabled(t *testing.T) {
	rc := &requestContext{policy: mustPolicy(t, []string{"postgres:latest"}, false)}

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

func TestPolicy_AllowsMultipleImages(t *testing.T) {
	p := mustPolicy(t, []string{"postgres:latest", "minio/minio:latest"}, true)
	if !p.matchesImage("minio/minio:latest") {
		t.Fatal("expected minio image to be allowed")
	}
	if !p.matchesNameAndTag("minio/minio", "latest") {
		t.Fatal("expected name/tag match for minio")
	}
	if p.matchesImage("redis:latest") {
		t.Fatal("expected redis to be blocked")
	}
}

func TestValidatePortBindings_MinioAllowed(t *testing.T) {
	bindings := map[string][]portBinding{
		"9000/tcp": []portBinding{{HostPort: ""}},
		"9001/tcp": []portBinding{{HostPort: ""}},
	}
	if err := validatePortBindings(bindings, []string{"9000/tcp", "9001/tcp"}); err != nil {
		t.Fatalf("expected allow, got %v", err)
	}
}

func TestValidatePortBindings_RejectsUnexpectedPort(t *testing.T) {
	bindings := map[string][]portBinding{
		"9000/tcp": []portBinding{{HostPort: ""}},
		"1234/tcp": []portBinding{{HostPort: ""}},
	}
	if err := validatePortBindings(bindings, []string{"9000/tcp", "9001/tcp"}); err == nil {
		t.Fatal("expected block for unexpected port")
	}
}

func mustPolicy(t *testing.T, images []string, allowPull bool) *policy {
	t.Helper()
	p, err := newPolicy(images, allowPull)
	if err != nil {
		t.Fatalf("newPolicy failed: %v", err)
	}
	return p
}
