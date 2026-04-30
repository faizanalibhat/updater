// Package agent contains the registration loop, heartbeat loop, and the
// self-update mechanism for the snapsec agent.
//
// The wire contract here matches the existing admin.snapsec.co
// "instances" controller (see Admin/backend/controllers/instances.controller.js
// and routes/v1/instances.routes.js):
//
//	POST <admin>/<base>/v1/instances              -> register
//	POST <admin>/<base>/v1/instances/heartbeat    -> heartbeat (body: { agentId })
//
// All admin responses are wrapped in {{ code, message, data }} (see
// Admin/backend/utils/apiResponse.js); we unwrap that envelope here and
// translate the action shape ({type, payload, _id}) into the agent's
// internal Action ({ID, Type, Params}).
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"snapsec-agent/internal/capabilities"
	"snapsec-agent/internal/config"
)

// Client talks to the admin control plane.
type Client struct {
	cfg  *config.Config
	http *http.Client
}

// New returns a Client configured against cfg.
func New(cfg *config.Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// ---- Wire types (admin contract) -----------------------------------------

type serverHardware struct {
	RAM  string `json:"ram,omitempty"`
	CPU  string `json:"cpu,omitempty"`
	Arch string `json:"arch,omitempty"`
	OS   string `json:"os,omitempty"`
}

type registerRequest struct {
	BaseURL        string         `json:"base_url"`
	Hostname       string         `json:"hostname,omitempty"`
	ServerHardware serverHardware `json:"server_hardware"`
	Status         string         `json:"status,omitempty"`
}

type heartbeatRequest struct {
	AgentID string       `json:"agentId"`
	Orgs    []orgLicence `json:"orgs,omitempty"`
}

// adminAction matches the embedded action subdoc on the Instance schema:
// { _id, type, payload, status }. We translate it into capabilities.Action.
type adminAction struct {
	ID      string         `json:"_id"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
	Status  string         `json:"status,omitempty"`
}

// envelope unwraps admin's apiResponse.successResponseWithData shape.
type envelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type registerResponseData struct {
	ID string `json:"id"`
}

type heartbeatResponseData struct {
	Actions []adminAction `json:"actions"`

	// The admin server doesn't currently emit these, but parsing them
	// optionally lets us turn on self-update / interval overrides as soon
	// as the server starts including them in the heartbeat envelope.
	LatestVersion            string `json:"latest_version,omitempty"`
	DownloadURL              string `json:"download_url,omitempty"`
	DownloadSHA256           string `json:"download_sha256,omitempty"`
	HeartbeatIntervalSeconds int    `json:"heartbeat_interval_seconds,omitempty"`
}

// HeartbeatResponse is the agent-internal view returned by Heartbeat.
type HeartbeatResponse struct {
	Actions                  []capabilities.Action
	LatestVersion            string
	DownloadURL              string
	DownloadSHA256           string
	HeartbeatIntervalSeconds int
}

// ---- URL helpers ----------------------------------------------------------

func (c *Client) baseURL() string {
	return strings.TrimRight(c.cfg.AdminURL, "/") + "/" + strings.Trim(c.cfg.AdminBasePath, "/")
}

func (c *Client) registerURL() string  { return c.baseURL() }
func (c *Client) heartbeatURL() string { return c.baseURL() + "/heartbeat" }

// ---- Registration ---------------------------------------------------------

// EnsureRegistered makes sure the agent has an agent_id, registering with
// the admin server if necessary, then persists the id to config.
func (c *Client) EnsureRegistered(ctx context.Context, capNames []string, version string) error {
	if c.cfg.AgentID != "" {
		return nil
	}

	host, _ := os.Hostname()
	body := registerRequest{
		BaseURL:  c.cfg.BaseURL,
		Hostname: host,
		ServerHardware: serverHardware{
			RAM:  detectRAM(),
			CPU:  strconv.Itoa(runtime.NumCPU()) + " Cores",
			Arch: runtime.GOARCH,
			OS:   runtime.GOOS,
		},
		Status: "active",
	}

	var env envelope[registerResponseData]
	if err := c.postJSON(ctx, c.registerURL(), body, &env); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	if env.Data.ID == "" {
		return fmt.Errorf("register: empty id in response")
	}
	if err := c.cfg.SetAgentID(env.Data.ID); err != nil {
		return fmt.Errorf("persist agent_id: %w", err)
	}
	return nil
}

// ---- Heartbeat ------------------------------------------------------------

// Heartbeat sends a single heartbeat and returns the server response.
//
// `capNames` and `lastResults` are accepted for API compatibility with the
// loop, but the current admin server does not consume them; only `agentId`
// is sent on the wire.
func (c *Client) Heartbeat(ctx context.Context, capNames, version string, lastResults []capabilities.Result) (*HeartbeatResponse, error) {
	mongoURI, mongoDB := c.cfg.MongoConnection()
	body := heartbeatRequest{
		AgentID: c.cfg.AgentID,
		Orgs:    collectOrgLicences(ctx, mongoURI, mongoDB),
	}

	var env envelope[heartbeatResponseData]
	if err := c.postJSON(ctx, c.heartbeatURL(), body, &env); err != nil {
		return nil, err
	}

	out := &HeartbeatResponse{
		LatestVersion:            env.Data.LatestVersion,
		DownloadURL:              env.Data.DownloadURL,
		DownloadSHA256:           env.Data.DownloadSHA256,
		HeartbeatIntervalSeconds: env.Data.HeartbeatIntervalSeconds,
	}
	for i, a := range env.Data.Actions {
		id := a.ID
		if id == "" {
			id = fmt.Sprintf("hb-%d-%d", time.Now().Unix(), i)
		}
		out.Actions = append(out.Actions, capabilities.Action{
			ID:     id,
			Type:   a.Type,
			Params: a.Payload,
		})
	}
	return out, nil
}

// ---- HTTP plumbing --------------------------------------------------------

func (c *Client) postJSON(ctx context.Context, url string, in any, out any) error {
	buf, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "snapsec-agent")
	if c.cfg.AgentID != "" {
		req.Header.Set("X-Agent-ID", c.cfg.AgentID)
	}
	if c.cfg.CFAccessClientID != "" && c.cfg.CFAccessClientSecret != "" {
		req.Header.Set("CF-Access-Client-Id", c.cfg.CFAccessClientID)
		req.Header.Set("CF-Access-Client-Secret", c.cfg.CFAccessClientSecret)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

// detectRAM returns the host's total RAM as a human-readable string
// (e.g. "16 GB"). Best-effort: returns "" if /proc/meminfo can't be read.
func detectRAM() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return ""
		}
		kb, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return ""
		}
		gb := kb / 1024 / 1024
		return fmt.Sprintf("%.0f GB", gb)
	}
	return ""
}
