// Copyright (c) 2026 Pedro Datorre
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func fakeInstaller(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			var request struct {
				ID     *int64 `json:"id"`
				Method string `json:"method"`
			}
			if err := conn.ReadJSON(&request); err != nil {
				return
			}

			switch request.Method {
			case "system_info":
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0", "id": request.ID,
					"result": map[string]any{
						"version": "25.10.5", "efi": false, "dtb": false,
						"installation_running": false, "installation_completed": false,
						"installation_error": nil,
					},
				})
			case "install":
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0", "method": "installation_progress",
					"params": []any{map[string]any{"progress": 0.5, "message": "half way"}},
				})
				_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": nil})
			case "shutdown":
				_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": nil})
				return
			default:
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0", "id": request.ID,
					"error": map[string]any{"code": -32601, "message": "method not found"},
				})
			}
		}
	}))
}

func TestNormalizeEndpoint(t *testing.T) {
	cases := map[string]string{
		"truenas-installer.local": "ws://truenas-installer.local:80/ws",
		"host:8080":               "ws://host:8080/ws",
		"http://host":             "ws://host:80/ws",
		"https://host/api":        "wss://host:443/api",
		"ws://host/ws":            "ws://host:80/ws",
	}
	for input, want := range cases {
		got, err := normalizeEndpoint(input)
		if err != nil {
			t.Fatalf("normalizeEndpoint(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", input, got, want)
		}
	}

	if _, err := normalizeEndpoint(""); err == nil {
		t.Error("expected an error for an empty endpoint")
	}
	if _, err := normalizeEndpoint("ftp://host"); err == nil {
		t.Error("expected an error for an unsupported scheme")
	}
}

func TestCallAndProgress(t *testing.T) {
	server := fakeInstaller(t)
	defer server.Close()

	cl, err := New(server.URL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx := context.Background()

	info, err := cl.SystemInfo(ctx)
	if err != nil {
		t.Fatalf("system_info: %v", err)
	}
	if info.Version != "25.10.5" || info.EFI {
		t.Errorf("unexpected system_info: %+v", info)
	}

	var progress []float64
	err = cl.Install(ctx, InstallRequest{
		Disks:          []string{"sda"},
		SetPMBR:        true,
		Authentication: &Authentication{Username: "truenas_admin", Password: "secret"},
	}, func(value float64, _ string) {
		progress = append(progress, value)
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(progress) != 1 || progress[0] != 0.5 {
		t.Errorf("unexpected progress notifications: %v", progress)
	}

	if _, err := cl.ListNetworkInterfaces(ctx); err == nil {
		t.Error("expected the JSON-RPC error to be surfaced")
	}
}

func TestConnectWithRetryFailsFastOnBadHandshake(t *testing.T) {
	// Make the 443 probe deterministic: point it at a closed port.
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := closed.Addr().(*net.TCPAddr).Port
	_ = closed.Close()
	oldPort := installedProbePort
	installedProbePort = strconv.Itoa(closedPort)
	defer func() { installedProbePort = oldPort }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not the installer", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cl, err := New(server.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err = cl.ConnectWithRetry(context.Background(), 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error")
	}
	var permanent *PermanentError
	if !errors.As(err, &permanent) {
		t.Fatalf("expected PermanentError, got %T: %v", err, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("took %s, expected a fail-fast error", elapsed)
	}
}

func TestConnectWithRetryAdoptsInstalledWithRedirect(t *testing.T) {
	// The installed TrueNAS redirects HTTP to HTTPS on port 80.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.invalid/", http.StatusFound)
	}))
	defer server.Close()

	// An open port stands in for TrueNAS on 443.
	installed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer installed.Close()

	oldPort := installedProbePort
	installedProbePort = strconv.Itoa(installed.Addr().(*net.TCPAddr).Port)
	defer func() { installedProbePort = oldPort }()

	cl, err := New(server.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err = cl.ConnectWithRetry(context.Background(), 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error")
	}
	var alreadyInstalled *AlreadyInstalledError
	if !errors.As(err, &alreadyInstalled) {
		t.Fatalf("expected AlreadyInstalledError, got %T: %v", err, err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("took %s, expected a fail-fast error", elapsed)
	}
}

func TestConnectWithRetryDetectsInstalled(t *testing.T) {
	// An open port that stands in for the installed TrueNAS on 443.
	installed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer installed.Close()

	oldPort := installedProbePort
	installedProbePort = strconv.Itoa(installed.Addr().(*net.TCPAddr).Port)
	defer func() { installedProbePort = oldPort }()

	// A closed port stands in for the installer endpoint.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	wsPort := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	cl, err := New(fmt.Sprintf("ws://127.0.0.1:%d/ws", wsPort), time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err = cl.ConnectWithRetry(context.Background(), 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error")
	}
	var alreadyInstalled *AlreadyInstalledError
	if !errors.As(err, &alreadyInstalled) {
		t.Fatalf("expected AlreadyInstalledError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "running TrueNAS") {
		t.Fatalf("unexpected message: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("took %s, expected a fail-fast error", elapsed)
	}
}

func TestProbeInstalled(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/current" {
			http.NotFound(w, r)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			var request struct {
				ID     *int64 `json:"id"`
				Method string `json:"method"`
			}
			if err := conn.ReadJSON(&request); err != nil {
				return
			}

			var result any
			switch request.Method {
			case "auth.login":
				result = true
			case "system.info":
				result = map[string]any{"version": "25.10.5"}
				_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
				return
			default:
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0", "id": request.ID,
					"error": map[string]any{"code": -32601, "message": "method not found"},
				})
				continue
			}
			_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	oldPort := truenasAPIPort
	truenasAPIPort = u.Port()
	defer func() { truenasAPIPort = oldPort }()

	version, err := ProbeInstalled(context.Background(), u.Hostname(), "truenas_admin", "secret123", true, 5*time.Second)
	if err != nil {
		t.Fatalf("ProbeInstalled: %v", err)
	}
	if version != "25.10.5" {
		t.Fatalf("version = %q, want 25.10.5", version)
	}
}
