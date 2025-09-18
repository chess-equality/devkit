package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	dockerAPIVersionPattern = regexp.MustCompile(`^/v[0-9]+\.[0-9]+/`)

	errForbidden        = errors.New("forbidden")
	errUnknownContainer = errors.New("container not managed by broker")
)

type brokerConfig struct {
	ListenAddr      string
	UpstreamAddr    string
	AllowedImage    string
	AllowedTag      string
	AllowedEnv      []string
	AllowImagePulls bool
	LogLevel        string
	AttachNetworks  []string
}

type containerRegistry struct {
	sync.Mutex
	known map[string]containerRecord
}

type containerRecord struct {
	ID   string
	Name string
}

func newContainerRegistry() *containerRegistry {
	return &containerRegistry{known: make(map[string]containerRecord)}
}

func (c *containerRegistry) add(id, name string) {
	c.Lock()
	defer c.Unlock()
	c.known[id] = containerRecord{ID: id, Name: name}
	if name != "" {
		c.known[name] = containerRecord{ID: id, Name: name}
	}
}

func (c *containerRegistry) remove(identifier string) {
	c.Lock()
	defer c.Unlock()
	for key, rec := range c.known {
		if rec.ID == identifier || rec.Name == identifier {
			delete(c.known, key)
		}
	}
}

func (c *containerRegistry) match(identifier string) (containerRecord, bool) {
	c.Lock()
	defer c.Unlock()

	for _, rec := range c.known {
		if rec.ID == identifier || rec.Name == identifier || strings.HasPrefix(rec.ID, identifier) {
			return rec, true
		}
	}
	return containerRecord{}, false
}

type requestContext struct {
	policy     *policy
	client     *http.Client
	target     *url.URL
	containers *containerRegistry
	attachNets []string
}

type policy struct {
	allowedImage string
	allowedTag   string
	allowPull    bool
}

func (p *policy) allowedImageRef() string {
	if p.allowedTag == "" {
		return p.allowedImage
	}
	return fmt.Sprintf("%s:%s", p.allowedImage, p.allowedTag)
}

type containerCreateRequest struct {
	Image      string            `json:"Image"`
	Env        []string          `json:"Env"`
	Cmd        []string          `json:"Cmd"`
	Entrypoint json.RawMessage   `json:"Entrypoint"`
	HostConfig hostConfigSection `json:"HostConfig"`
}

type hostConfigSection struct {
	Binds           []string                 `json:"Binds"`
	CapAdd          []string                 `json:"CapAdd"`
	CapDrop         []string                 `json:"CapDrop"`
	Privileged      bool                     `json:"Privileged"`
	NetworkMode     string                   `json:"NetworkMode"`
	PublishAllPorts bool                     `json:"PublishAllPorts"`
	PortBindings    map[string][]portBinding `json:"PortBindings"`
	ExtraHosts      []string                 `json:"ExtraHosts"`
	IpcMode         string                   `json:"IpcMode"`
	PidMode         string                   `json:"PidMode"`
	SecurityOpt     []string                 `json:"SecurityOpt"`
	ReadonlyRootfs  bool                     `json:"ReadonlyRootfs"`
	Init            bool                     `json:"Init"`
}

type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type containerCreateResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

func loadConfig() brokerConfig {
	cfg := brokerConfig{
		ListenAddr:      getEnv("BROKER_LISTEN", "unix:///var/run/postgres-broker.sock"),
		UpstreamAddr:    getEnv("BROKER_UPSTREAM", "unix:///var/run/docker.sock"),
		AllowedImage:    getEnv("BROKER_ALLOWED_IMAGE", "postgres"),
		AllowedTag:      os.Getenv("BROKER_ALLOWED_TAG"),
		AllowImagePulls: getEnv("BROKER_ALLOW_PULLS", "false") == "true",
		LogLevel:        getEnv("BROKER_LOG_LEVEL", "info"),
	}
	if env := os.Getenv("BROKER_ALLOWED_ENV"); env != "" {
		cfg.AllowedEnv = strings.Split(env, ",")
	}
	if nets := os.Getenv("BROKER_ATTACH_NETWORKS"); nets != "" {
		for _, part := range strings.Split(nets, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				cfg.AttachNetworks = append(cfg.AttachNetworks, trimmed)
			}
		}
	}
	if net := strings.TrimSpace(os.Getenv("BROKER_ATTACH_NETWORK")); net != "" {
		cfg.AttachNetworks = append(cfg.AttachNetworks, net)
	}
	return cfg
}

func buildClient(upstream string) (*http.Client, *url.URL, error) {
	parsed, err := url.Parse(upstream)
	if err != nil {
		return nil, nil, err
	}

	transport := &http.Transport{DisableCompression: true}

	switch parsed.Scheme {
	case "unix", "":
		socketPath := parsed.Path
		if socketPath == "" {
			socketPath = upstream
		}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.DialTimeout("unix", socketPath, 10*time.Second)
		}
		return &http.Client{Transport: transport, Timeout: 0}, &url.URL{Scheme: "http", Host: "docker"}, nil
	case "tcp":
		host := parsed.Host
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			target := host
			if target == "" {
				target = addr
			}
			return net.DialTimeout("tcp", target, 10*time.Second)
		}
		return &http.Client{Transport: transport, Timeout: 0}, &url.URL{Scheme: "http", Host: host}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported upstream scheme %s", parsed.Scheme)
	}
}

func main() {
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})
	cfg := loadConfig()

	if level, err := log.ParseLevel(cfg.LogLevel); err == nil {
		log.SetLevel(level)
	} else {
		log.Warnf("invalid log level %s, defaulting to info", cfg.LogLevel)
	}

	listenProto, listenAddr := parseListenAddr(cfg.ListenAddr)
	if listenProto != "unix" {
		log.Fatalf("unsupported listen protocol: %s", listenProto)
	}
	if err := ensureSocketAbsent(listenAddr); err != nil {
		log.Fatalf("unable to prepare listen socket: %v", err)
	}

	policy := &policy{
		allowedImage: cfg.AllowedImage,
		allowedTag:   cfg.AllowedTag,
		allowPull:    cfg.AllowImagePulls,
	}

	client, targetURL, err := buildClient(cfg.UpstreamAddr)
	if err != nil {
		log.Fatalf("invalid upstream address: %v", err)
	}

	rc := &requestContext{
		policy:     policy,
		client:     client,
		target:     targetURL,
		containers: newContainerRegistry(),
		attachNets: cfg.AttachNetworks,
	}

	handler := http.HandlerFunc(rc.handle)

	server := &http.Server{
		Handler:           handler,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		ReadHeaderTimeout: 10 * time.Second,
	}

	listener, err := net.Listen(listenProto, listenAddr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", cfg.ListenAddr, err)
	}
	if listenProto == "unix" {
		if err := os.Chmod(listenAddr, 0o666); err != nil {
			log.WithError(err).Warn("failed to adjust socket permissions")
		}
	}
	log.Infof("postgres broker listening on %s forwarding to %s", cfg.ListenAddr, cfg.UpstreamAddr)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

func parseListenAddr(addr string) (string, string) {
	if strings.HasPrefix(addr, "unix://") {
		return "unix", strings.TrimPrefix(addr, "unix://")
	}
	if strings.HasPrefix(addr, "unix:") {
		return "unix", strings.TrimPrefix(addr, "unix:")
	}
	if strings.HasPrefix(addr, "/") {
		return "unix", addr
	}
	return "", addr
}

func ensureSocketAbsent(path string) error {
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepathDir(path), 0o755); err != nil {
		return err
	}
	return nil
}

func filepathDir(p string) string {
	if p == "" {
		return "."
	}
	return path.Dir(p)
}

func (rc *requestContext) handle(w http.ResponseWriter, r *http.Request) {
	log.WithFields(log.Fields{"method": r.Method, "path": r.URL.Path}).Debug("incoming request")
	if rc.handleNoop(w, r) {
		return
	}
	if err := rc.authorize(r); err != nil {
		switch {
		case errors.Is(err, errForbidden), errors.Is(err, errUnknownContainer):
			log.WithFields(log.Fields{"method": r.Method, "path": r.URL.Path}).Warn("blocked request")
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			log.WithError(err).Error("failed to authorize request")
			http.Error(w, "broker error", http.StatusInternalServerError)
		}
		return
	}

	resp, err := rc.forward(r)
	if err != nil {
		log.WithError(err).Error("forward error")
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if err := rc.postProcess(r, resp); err != nil {
		log.WithError(err).Error("post process error")
	}

	copyResponse(w, resp)
}

func (rc *requestContext) handleNoop(w http.ResponseWriter, r *http.Request) bool {
	cleanPath := stripVersionPrefix(r.URL.Path)
	if cleanPath == "/containers/prune" && r.Method == http.MethodPost {
		log.WithFields(log.Fields{"method": r.Method, "path": r.URL.Path}).Debug("responding to containers prune noop")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ContainersDeleted": []string{},
			"SpaceReclaimed":    0,
		})
		return true
	}
	if cleanPath == "/networks/prune" && r.Method == http.MethodPost {
		log.WithFields(log.Fields{"method": r.Method, "path": r.URL.Path}).Debug("responding to networks prune noop")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"NetworksDeleted": []string{},
		})
		return true
	}
	if cleanPath == "/volumes/prune" && r.Method == http.MethodPost {
		log.WithFields(log.Fields{"method": r.Method, "path": r.URL.Path}).Debug("responding to volumes prune noop")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"VolumesDeleted": []string{},
			"SpaceReclaimed": 0,
		})
		return true
	}
	if cleanPath == "/images/prune" && r.Method == http.MethodPost {
		log.WithFields(log.Fields{"method": r.Method, "path": r.URL.Path}).Debug("responding to images prune noop")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ImagesDeleted":  []string{},
			"SpaceReclaimed": 0,
		})
		return true
	}
	return false
}

func (rc *requestContext) forward(r *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()
	log.WithFields(log.Fields{"path": r.URL.Path, "method": r.Method, "len": len(body)}).Debug("forwarding request")

	clone := r.Clone(r.Context())
	if len(body) > 0 {
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.ContentLength = int64(len(body))
	} else {
		clone.Body = http.NoBody
		clone.ContentLength = 0
		clone.Header.Del("Content-Type")
	}
	clone.URL = rc.target.ResolveReference(&url.URL{Path: r.URL.Path, RawQuery: r.URL.RawQuery})
	clone.Host = "docker"
	clone.RequestURI = ""

	return rc.client.Do(clone)
}

func (rc *requestContext) postProcess(r *http.Request, resp *http.Response) error {
	if resp.StatusCode >= 300 {
		return nil
	}

	cleanPath := stripVersionPrefix(r.URL.Path)
	switch {
	case cleanPath == "/containers/create" && r.Method == http.MethodPost:
		id, err := rc.captureContainer(resp, r.URL.Query().Get("name"))
		if err != nil {
			return err
		}
		if id != "" {
			rc.attachContainerNetworks(id)
		}
		return nil
	case strings.HasPrefix(cleanPath, "/containers/") && r.Method == http.MethodDelete:
		identifier := containerIDFromPath(cleanPath)
		if identifier != "" {
			rc.containers.remove(identifier)
		}
	}

	return nil
}

func (rc *requestContext) captureContainer(resp *http.Response, requestedName string) (string, error) {
	bodyCopy, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	restored := io.NopCloser(bytes.NewReader(bodyCopy))
	resp.Body = restored

	var payload containerCreateResponse
	if err := json.Unmarshal(bodyCopy, &payload); err != nil {
		return "", err
	}
	if payload.ID != "" {
		rc.containers.add(payload.ID, requestedName)
	}
	return payload.ID, nil
}

func (rc *requestContext) attachContainerNetworks(containerID string) {
	if strings.TrimSpace(containerID) == "" {
		return
	}
	for _, netName := range rc.attachNets {
		name := strings.TrimSpace(netName)
		if name == "" {
			continue
		}
		if err := rc.connectNetwork(containerID, name); err != nil {
			log.WithFields(log.Fields{"container": containerID, "network": name, "error": err.Error()}).Warn("failed to attach network")
		}
	}
}

func (rc *requestContext) connectNetwork(containerID, network string) error {
	path := fmt.Sprintf("/networks/%s/connect", network)
	payload := map[string]string{"Container": containerID}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, rc.target.ResolveReference(&url.URL{Path: path}).String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := rc.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("network connect status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (rc *requestContext) authorize(r *http.Request) error {
	cleanPath := stripVersionPrefix(r.URL.Path)
	switch cleanPath {
	case "/_ping", "/version", "/info":
		return nil
	}

	switch {
	case cleanPath == "/images/json" && r.Method == http.MethodGet:
		return nil
	case cleanPath == "/images/create" && r.Method == http.MethodPost:
		return rc.authorizeImageCreate(r)
	case strings.HasPrefix(cleanPath, "/images/") && strings.HasSuffix(cleanPath, "/json") && r.Method == http.MethodGet:
		return rc.authorizeImageInspect(cleanPath)
	case cleanPath == "/containers/json" && r.Method == http.MethodGet:
		return nil
	case cleanPath == "/containers/create" && r.Method == http.MethodPost:
		return rc.authorizeContainerCreate(r)
	case strings.HasPrefix(cleanPath, "/containers/"):
		return rc.authorizeContainerAction(cleanPath)
	case strings.HasPrefix(cleanPath, "/networks/") && r.Method == http.MethodGet:
		return rc.authorizeNetworkInspect(cleanPath)
	default:
		return errForbidden
	}
}

func (rc *requestContext) authorizeImageCreate(r *http.Request) error {
	if !rc.policy.allowPull {
		return errForbidden
	}
	ref := rc.policy.allowedImage
	if tag := rc.policy.allowedTag; tag != "" {
		ref = fmt.Sprintf("%s:%s", rc.policy.allowedImage, tag)
	}
	fromImage := r.URL.Query().Get("fromImage")
	tag := r.URL.Query().Get("tag")

	expectedImage := rc.policy.allowedImage
	if fromImage != expectedImage {
		return errForbidden
	}
	if rc.policy.allowedTag != "" && tag != rc.policy.allowedTag {
		return errForbidden
	}
	log.WithField("image", ref).Debug("allowing image pull")
	return nil
}

func (rc *requestContext) authorizeContainerCreate(r *http.Request) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	var payload containerCreateRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		return errForbidden
	}

	if payload.Image != rc.policy.allowedImageRef() {
		log.WithFields(log.Fields{"image": payload.Image, "allowed": rc.policy.allowedImageRef()}).Warn("blocked container create: image mismatch")
		return errForbidden
	}
	log.WithFields(log.Fields{
		"image":        payload.Image,
		"env":          payload.Env,
		"portBindings": payload.HostConfig.PortBindings,
		"networkMode":  payload.HostConfig.NetworkMode,
	}).Debug("inspected container create payload")

	if payload.HostConfig.Privileged {
		log.Warn("blocked container create: privileged requested")
		return errForbidden
	}

	if nm := payload.HostConfig.NetworkMode; nm != "" && nm != "bridge" && nm != "default" {
		log.WithField("network_mode", nm).Warn("blocked container create: network mode disallowed")
		return errForbidden
	}

	if len(payload.HostConfig.Binds) > 0 || len(payload.HostConfig.CapAdd) > 0 || payload.HostConfig.PublishAllPorts {
		log.Warn("blocked container create: binds/caps/publish detected")
		return errForbidden
	}

	if err := validatePortBindings(payload.HostConfig.PortBindings); err != nil {
		log.WithError(err).Warn("blocked container create: port bindings")
		return err
	}

	if len(payload.HostConfig.ExtraHosts) > 0 || payload.HostConfig.IpcMode != "" || payload.HostConfig.PidMode != "" || len(payload.HostConfig.SecurityOpt) > 0 {
		log.Warn("blocked container create: extra host/ipc/pid/security opts")
		return errForbidden
	}

	log.WithField("image", payload.Image).Debug("allowing container create")
	return nil
}

func validatePortBindings(bindings map[string][]portBinding) error {
	if bindings == nil {
		return nil
	}
	if len(bindings) == 0 {
		return nil
	}
	if len(bindings) > 1 {
		return fmt.Errorf("%w: multiple bindings", errForbidden)
	}
	for key, values := range bindings {
		if key != "5432/tcp" {
			return fmt.Errorf("%w: unexpected container port %s", errForbidden, key)
		}
		for _, v := range values {
			if v.HostIP != "" && v.HostIP != "0.0.0.0" && v.HostIP != "::" {
				return fmt.Errorf("%w: disallowed host ip %s", errForbidden, v.HostIP)
			}
			if v.HostPort != "" && v.HostPort != "5432" {
				// Allow Docker to choose random host port when empty; forbid fixed non-Postgres ports.
				return fmt.Errorf("%w: disallowed host port %s", errForbidden, v.HostPort)
			}
		}
	}
	return nil
}

func (rc *requestContext) authorizeContainerAction(cleanPath string) error {
	identifier := containerIDFromPath(cleanPath)
	if identifier == "" {
		return errForbidden
	}
	if _, ok := rc.containers.match(identifier); !ok {
		return errUnknownContainer
	}
	log.WithField("id", identifier).Debug("allowing container action")
	return nil
}

func (rc *requestContext) authorizeNetworkInspect(cleanPath string) error {
	identifier := resourceIDFromPath(cleanPath)
	switch identifier {
	case "bridge", "host", "none":
		log.WithField("network", identifier).Debug("allowing network inspect")
		return nil
	default:
		return errForbidden
	}
}

func (rc *requestContext) authorizeImageInspect(cleanPath string) error {
	identifier := strings.TrimPrefix(cleanPath, "/images/")
	identifier = strings.TrimSuffix(identifier, "/json")
	identifier = strings.Trim(identifier, "/")
	if identifier == "" {
		return errForbidden
	}
	if strings.HasPrefix(identifier, "sha256:") {
		log.WithField("image", identifier).Debug("allowing digest inspect")
		return nil
	}
	allowedRef := rc.policy.allowedImageRef()
	allowedImage := rc.policy.allowedImage
	if matchesAllowedImage(identifier, allowedImage, allowedRef) {
		log.WithField("image", identifier).Debug("allowing image inspect")
		return nil
	}
	return errForbidden
}

func containerIDFromPath(cleanPath string) string {
	return resourceIDFromPath(cleanPath)
}

func resourceIDFromPath(cleanPath string) string {
	parts := strings.Split(cleanPath, "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

func matchesAllowedImage(candidate, allowedImage, allowedRef string) bool {
	allowed := map[string]struct{}{}
	addImageVariants(allowed, allowedImage)
	addImageVariants(allowed, allowedRef)
	_, ok := allowed[candidate]
	return ok
}

func addImageVariants(set map[string]struct{}, val string) {
	if val == "" {
		return
	}
	for _, variant := range []string{val, "library/" + val, "docker.io/" + val, "docker.io/library/" + val} {
		set[variant] = struct{}{}
	}
}

func stripVersionPrefix(p string) string {
	return dockerAPIVersionPattern.ReplaceAllString(p, "/")
}

type flushWriter struct {
	w  http.ResponseWriter
	fl http.Flusher
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err == nil {
		fw.fl.Flush()
	}
	return n, err
}

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	writer := io.Writer(w)
	if fl, ok := w.(http.Flusher); ok {
		writer = &flushWriter{w: w, fl: fl}
	}
	_, _ = io.Copy(writer, resp.Body)
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
