// Package docker is a tiny Docker Engine API client that speaks raw HTTP.
//
// It deliberately avoids the official (heavy) client so it stays compatible
// with restricted socket-proxies (e.g. Tecnativa docker-socket-proxy) that do
// not implement version negotiation. Only the handful of endpoints we need are
// implemented.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to a Docker Engine API endpoint.
type Client struct {
	base       string // e.g. http://host:2375  (unix handled via transport)
	apiPrefix  string // e.g. /v1.43 or ""
	httpClient *http.Client
}

// New builds a client for the given host. Host may be:
//
//	http://host:2375      (socket-proxy / TCP)
//	https://host:2376
//	unix:///var/run/docker.sock
func New(host, apiVersion string, timeout time.Duration) (*Client, error) {
	if host == "" {
		return nil, fmt.Errorf("leerer Docker-Host")
	}
	c := &Client{httpClient: &http.Client{Timeout: timeout}}
	if apiVersion != "" {
		v := apiVersion
		if !strings.HasPrefix(v, "v") {
			v = "v" + v
		}
		c.apiPrefix = "/" + v
	}

	switch {
	case strings.HasPrefix(host, "unix://"):
		sock := strings.TrimPrefix(host, "unix://")
		c.base = "http://unix"
		c.httpClient.Transport = &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		}
	case strings.HasPrefix(host, "http://"), strings.HasPrefix(host, "https://"):
		c.base = strings.TrimRight(host, "/")
	default:
		// bare host[:port] -> assume http
		c.base = "http://" + strings.TrimRight(host, "/")
	}
	return c, nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, out any) error {
	full := c.base + c.apiPrefix + path
	if len(q) > 0 {
		full += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, full, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Method: method, Path: path, Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return nil
}

// APIError is returned for non-2xx responses.
type APIError struct {
	Method string
	Path   string
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("docker API %s %s -> HTTP %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// Ping verifies connectivity via /_ping (falls back to /info).
func (c *Client) Ping(ctx context.Context) error {
	// /_ping returns "OK" as text, so don't decode.
	if err := c.do(ctx, http.MethodGet, "/_ping", nil, nil); err == nil {
		return nil
	}
	var info map[string]any
	return c.do(ctx, http.MethodGet, "/info", nil, &info)
}

// ---- Types (only the fields we use) ----

// Image mirrors a subset of /images/json entries.
type Image struct {
	ID          string            `json:"Id"`
	ParentID    string            `json:"ParentId"`
	RepoTags    []string          `json:"RepoTags"`
	RepoDigests []string          `json:"RepoDigests"`
	Created     int64             `json:"Created"` // unix seconds
	Size        int64             `json:"Size"`
	Labels      map[string]string `json:"Labels"`
}

// Dangling reports whether the image has no usable repo tag.
func (i Image) Dangling() bool {
	if len(i.RepoTags) == 0 {
		return true
	}
	for _, t := range i.RepoTags {
		if t != "<none>:<none>" && t != "" {
			return false
		}
	}
	return true
}

// Ref returns a human-friendly reference for display.
func (i Image) Ref() string {
	for _, t := range i.RepoTags {
		if t != "<none>:<none>" && t != "" {
			return t
		}
	}
	return "<none>"
}

// Container mirrors a subset of /containers/json entries.
type Container struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Created int64             `json:"Created"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Labels  map[string]string `json:"Labels"`
	SizeRw  int64             `json:"SizeRw"`
}

// Name returns the primary container name without the leading slash.
func (c Container) Name() string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID[:min(12, len(c.ID))]
}

// Volume mirrors a subset of /volumes entries.
type Volume struct {
	Name      string            `json:"Name"`
	Driver    string            `json:"Driver"`
	CreatedAt string            `json:"CreatedAt"` // RFC3339
	Labels    map[string]string `json:"Labels"`
	// UsageData is only populated by /system/df.
	UsageData *struct {
		Size     int64 `json:"Size"`
		RefCount int64 `json:"RefCount"`
	} `json:"UsageData"`
}

type volumeList struct {
	Volumes  []Volume `json:"Volumes"`
	Warnings []string `json:"Warnings"`
}

// Network mirrors a subset of /networks entries.
type Network struct {
	ID         string            `json:"Id"`
	Name       string            `json:"Name"`
	Created    string            `json:"Created"` // RFC3339
	Scope      string            `json:"Scope"`
	Labels     map[string]string `json:"Labels"`
	Containers map[string]any    `json:"Containers"`
}

// BuildCacheRecord mirrors /system/df BuildCache entries.
type BuildCacheRecord struct {
	ID          string `json:"ID"`
	Type        string `json:"Type"`
	Size        int64  `json:"Size"`
	InUse       bool   `json:"InUse"`
	Shared      bool   `json:"Shared"`
	CreatedAt   string `json:"CreatedAt"`
	LastUsedAt  string `json:"LastUsedAt"`
	Description string `json:"Description"`
}

// DiskUsage mirrors the parts of /system/df we care about.
type DiskUsage struct {
	LayersSize int64              `json:"LayersSize"`
	Volumes    []Volume           `json:"Volumes"`
	BuildCache []BuildCacheRecord `json:"BuildCache"`
}

// ---- List calls ----

// ListImages returns all images (all=true so intermediate layers included when tagged).
func (c *Client) ListImages(ctx context.Context) ([]Image, error) {
	var out []Image
	q := url.Values{"all": {"false"}}
	err := c.do(ctx, http.MethodGet, "/images/json", q, &out)
	return out, err
}

// ListContainers returns all containers (running and stopped).
func (c *Client) ListContainers(ctx context.Context) ([]Container, error) {
	var out []Container
	q := url.Values{"all": {"true"}, "size": {"true"}}
	err := c.do(ctx, http.MethodGet, "/containers/json", q, &out)
	return out, err
}

// ListVolumes returns all volumes.
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	var out volumeList
	err := c.do(ctx, http.MethodGet, "/volumes", nil, &out)
	return out.Volumes, err
}

// ListNetworks returns all networks.
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	var out []Network
	err := c.do(ctx, http.MethodGet, "/networks", nil, &out)
	return out, err
}

// DiskUsage returns /system/df (used mainly for volume sizes + build cache).
func (c *Client) DiskUsage(ctx context.Context) (*DiskUsage, error) {
	var out DiskUsage
	err := c.do(ctx, http.MethodGet, "/system/df", nil, &out)
	return &out, err
}

// ---- Delete calls ----

// RemoveImage deletes an image by ID.
func (c *Client) RemoveImage(ctx context.Context, id string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	return c.do(ctx, http.MethodDelete, "/images/"+id, q, nil)
}

// RemoveVolume deletes a volume by name.
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	return c.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), q, nil)
}

// RemoveContainer deletes a (stopped) container by ID.
func (c *Client) RemoveContainer(ctx context.Context, id string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	return c.do(ctx, http.MethodDelete, "/containers/"+id, q, nil)
}

// RemoveNetwork deletes a network by ID.
func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/networks/"+id, nil, nil)
}

// PruneResult is the response shape of build/prune.
type PruneResult struct {
	CachesDeleted  []string `json:"CachesDeleted"`
	SpaceReclaimed int64    `json:"SpaceReclaimed"`
}

// PruneBuildCache prunes the build cache. keepUntil (e.g. "24h") restricts to
// records older than that; empty means all reclaimable cache.
func (c *Client) PruneBuildCache(ctx context.Context, keepUntil string) (*PruneResult, error) {
	q := url.Values{}
	if keepUntil != "" {
		filters := fmt.Sprintf(`{"until":[%q]}`, keepUntil)
		q.Set("filters", filters)
	}
	var out PruneResult
	err := c.do(ctx, http.MethodPost, "/build/prune", q, &out)
	return &out, err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
