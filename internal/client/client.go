// Copyright (c) 2026 Pedro Datorre
// SPDX-License-Identifier: MPL-2.0

// Package client implements the JSON-RPC 2.0 / WebSocket protocol exposed by
// truenas-installer.service on a booted TrueNAS SCALE installation ISO.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return fmt.Sprintf("%s (code %d, data %s)", e.Message, e.Code, e.Data)
	}
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
}

// ProgressFunc receives installation_progress notifications.
type ProgressFunc func(progress float64, message string)

// Client is a single JSON-RPC connection to the installer.
type Client struct {
	endpoint string
	timeout  time.Duration

	mu     sync.Mutex
	conn   *websocket.Conn
	nextID int64
}

// New builds a client. endpoint may be "host", "host:port", "ws://host/ws",
// "http://host:80/ws", ... and is normalised to a WebSocket URL.
func New(endpoint string, timeout time.Duration) (*Client, error) {
	normalized, err := normalizeEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	return &Client{endpoint: normalized, timeout: timeout}, nil
}

// Endpoint returns the normalised WebSocket URL.
func (c *Client) Endpoint() string { return c.endpoint }

func normalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("endpoint must not be empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "ws://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint %q: %w", raw, err)
	}

	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported endpoint scheme %q (use host, ws:// or wss://)", u.Scheme)
	}

	if u.Host == "" {
		return "", fmt.Errorf("invalid endpoint %q: missing host", raw)
	}
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "wss" {
			port = "443"
		}
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/ws"
	}
	return u.String(), nil
}

// Connect opens the WebSocket connection (idempotent).
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connectLocked(ctx)
}

// ConnectWithRetry retries Connect every interval until ctx is done. It is used
// by resources that may run right after the installer VM has booted.
func (c *Client) ConnectWithRetry(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := c.Connect(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return fmt.Errorf("connect to %s: %w", c.endpoint, lastErr)
		case <-time.After(interval):
		}
	}
}

func (c *Client) connectLocked(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	conn, _, err := dialer.DialContext(ctx, c.endpoint, nil)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", c.endpoint, err)
	}
	c.conn = conn
	return nil
}

// Close closes the WebSocket connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// Call performs a JSON-RPC request and returns its raw result. Server
// notifications are ignored, except installation_progress which is forwarded
// to onProgress.
func (c *Client) Call(ctx context.Context, method string, params []any, onProgress ProgressFunc) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.connectLocked(ctx); err != nil {
		return nil, err
	}
	if params == nil {
		params = []any{}
	}

	c.nextID++
	id := c.nextID
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	deadline := time.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	if err := c.conn.SetWriteDeadline(deadline); err != nil {
		return nil, err
	}
	if err := c.conn.WriteJSON(request); err != nil {
		return nil, fmt.Errorf("write %s request: %w", method, err)
	}

	for {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}

		var message struct {
			ID     *int64            `json:"id"`
			Method string            `json:"method"`
			Result json.RawMessage   `json:"result"`
			Error  *RPCError         `json:"error"`
			Params []json.RawMessage `json:"params"`
		}
		if err := c.conn.ReadJSON(&message); err != nil {
			return nil, fmt.Errorf("read %s response: %w", method, err)
		}

		if message.ID != nil && *message.ID == id {
			if message.Error != nil {
				return nil, message.Error
			}
			return message.Result, nil
		}

		if message.Method == "installation_progress" && onProgress != nil && len(message.Params) > 0 {
			var progress struct {
				Progress float64 `json:"progress"`
				Message  string  `json:"message"`
			}
			if err := json.Unmarshal(message.Params[0], &progress); err == nil {
				onProgress(progress.Progress, progress.Message)
			}
		}
	}
}

// SystemInfo mirrors the installer's system_info result.
type SystemInfo struct {
	InstallationRunning   bool    `json:"installation_running"`
	InstallationCompleted bool    `json:"installation_completed"`
	InstallationError     *string `json:"installation_error"`
	Version               string  `json:"version"`
	EFI                   bool    `json:"efi"`
	DTB                   bool    `json:"dtb"`
}

// SystemInfo calls system_info.
func (c *Client) SystemInfo(ctx context.Context) (SystemInfo, error) {
	var info SystemInfo
	raw, err := c.Call(ctx, "system_info", nil, nil)
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(raw, &info)
	return info, err
}

// Disk mirrors the installer's list_disks entries.
type Disk struct {
	Name       string      `json:"name"`
	Size       int64       `json:"size"`
	Model      string      `json:"model"`
	Label      string      `json:"label"`
	ZFSMembers []ZFSMember `json:"zfs_members"`
	Removable  bool        `json:"removable"`
}

// ZFSMember is a ZFS vdev found on a disk.
type ZFSMember struct {
	Name string `json:"name"`
	Pool string `json:"pool"`
}

// ListDisks calls list_disks.
func (c *Client) ListDisks(ctx context.Context) ([]Disk, error) {
	var disks []Disk
	raw, err := c.Call(ctx, "list_disks", nil, nil)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &disks)
	return disks, err
}

// NetworkInterface mirrors list_network_interfaces.
type NetworkInterface struct {
	Name string `json:"name"`
}

// ListNetworkInterfaces calls list_network_interfaces.
func (c *Client) ListNetworkInterfaces(ctx context.Context) ([]NetworkInterface, error) {
	var interfaces []NetworkInterface
	raw, err := c.Call(ctx, "list_network_interfaces", nil, nil)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &interfaces)
	return interfaces, err
}

// Authentication is the installed system's admin account.
type Authentication struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Alias is a static address assigned in post_install.
type Alias struct {
	Type    string `json:"type"`
	Address string `json:"address"`
	Netmask int    `json:"netmask"`
}

// NetworkInterfaceConfig configures an interface on the installed system.
type NetworkInterfaceConfig struct {
	Name     string  `json:"name"`
	Aliases  []Alias `json:"aliases,omitempty"`
	IPv4DHCP *bool   `json:"ipv4_dhcp,omitempty"`
	IPv6Auto *bool   `json:"ipv6_auto,omitempty"`
}

// PostInstall is the installer's post_install block.
type PostInstall struct {
	NetworkInterfaces []NetworkInterfaceConfig `json:"network_interfaces,omitempty"`
}

// InstallRequest is the installer install method payload.
type InstallRequest struct {
	Disks          []string        `json:"disks"`
	WipeDisks      []string        `json:"wipe_disks,omitempty"`
	SetPMBR        bool            `json:"set_pmbr"`
	Authentication *Authentication `json:"authentication"`
	PostInstall    *PostInstall    `json:"post_install,omitempty"`
}

// Install calls install and blocks until the installer reports completion.
func (c *Client) Install(ctx context.Context, request InstallRequest, onProgress ProgressFunc) error {
	_, err := c.Call(ctx, "install", []any{request}, onProgress)
	return err
}

// Shutdown calls shutdown. The connection is expected to drop.
func (c *Client) Shutdown(ctx context.Context) error {
	_, err := c.Call(ctx, "shutdown", nil, nil)
	return err
}

// Reboot calls reboot. The connection is expected to drop.
func (c *Client) Reboot(ctx context.Context) error {
	_, err := c.Call(ctx, "reboot", nil, nil)
	return err
}

// TNCConfig mirrors the TrueNAS Connect configuration.
type TNCConfig struct {
	Enabled                  bool    `json:"enabled"`
	AccountServiceBaseURL    *string `json:"account_service_base_url"`
	LecaServiceBaseURL       *string `json:"leca_service_base_url"`
	HeartbeatServiceBaseURL  *string `json:"heartbeat_service_base_url"`
	TNCBaseURL               *string `json:"tnc_base_url"`
	ClaimToken               *string `json:"claim_token"`
	ClaimTokenExpiration     *int64  `json:"claim_token_expiration"`
	SystemID                 *string `json:"system_id"`
	TruenasVersion           *string `json:"truenas_version"`
	InitializationInProgress bool    `json:"initialization_in_progress"`
	InitializationCompleted  bool    `json:"initialization_completed"`
	InitializationError      *string `json:"initialization_error"`
}

// TNCConfigRequest is the configure_tnc payload.
type TNCConfigRequest struct {
	Enabled                 bool   `json:"enabled"`
	AccountServiceBaseURL   string `json:"account_service_base_url,omitempty"`
	LecaServiceBaseURL      string `json:"leca_service_base_url,omitempty"`
	HeartbeatServiceBaseURL string `json:"heartbeat_service_base_url,omitempty"`
	TNCBaseURL              string `json:"tnc_base_url,omitempty"`
}

// TNCConfig calls tnc_config.
func (c *Client) TNCConfig(ctx context.Context) (TNCConfig, error) {
	var config TNCConfig
	raw, err := c.Call(ctx, "tnc_config", nil, nil)
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(raw, &config)
	return config, err
}

// ConfigureTNC calls configure_tnc. It can only be called once per boot.
func (c *Client) ConfigureTNC(ctx context.Context, request TNCConfigRequest) (TNCConfig, error) {
	var config TNCConfig
	raw, err := c.Call(ctx, "configure_tnc", []any{request}, nil)
	if err != nil {
		return config, err
	}
	err = json.Unmarshal(raw, &config)
	return config, err
}

// TNCRegistrationURI calls tnc_registration_uri.
func (c *Client) TNCRegistrationURI(ctx context.Context) (string, error) {
	var uri string
	raw, err := c.Call(ctx, "tnc_registration_uri", nil, nil)
	if err != nil {
		return "", err
	}
	err = json.Unmarshal(raw, &uri)
	return uri, err
}
