package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ShellRequest holds optional parameters for creating a shell session.
type ShellRequest struct {
	// Command to run in the shared session (default: current user's shell).
	Command string `json:"command,omitempty"`
	// Server is the upterm server address (default: ssh://uptermd.upterm.dev:22).
	Server string `json:"server,omitempty"`
}

// ShellResponse contains the upterm session details.
type ShellResponse struct {
	Success    bool              `json:"success"`
	SessionID  string            `json:"sessionId,omitempty"`
	SSH        string            `json:"ssh,omitempty"`
	Host       string            `json:"host,omitempty"`
	Command    string            `json:"command,omitempty"`
	Raw        map[string]any    `json:"raw,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// HandleShell starts an upterm session and returns connection details.
func HandleShell(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req ShellRequest
	if r.Body != nil {
		defer r.Body.Close()
		json.NewDecoder(r.Body).Decode(&req)
	}

	// Check if upterm is installed
	uptermPath, err := exec.LookPath("upterm")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable,
			"upterm not found in PATH. Install it: https://github.com/owenthereal/upterm#installation")
		return
	}
	log.Printf("using upterm at: %s", uptermPath)

	command := req.Command
	if command == "" {
		command = os.Getenv("SHELL")
		if command == "" {
			command = "/bin/bash"
		}
	}

	server := req.Server
	if server == "" {
		server = os.Getenv("UPTERM_SERVER")
		if server == "" {
			server = "ssh://uptermd.upterm.dev:22"
		}
	}

	sessionTag := uuid.New().String()[:8]

	// Start upterm host in the background with --accept (auto-accept connections)
	hostArgs := []string{
		"host",
		"--accept",
		"--server", server,
		"--", command,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, uptermPath, hostArgs...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("UPTERM_SESSION_TAG=%s", sessionTag))

	// Capture output for debugging
	var hostStderr bytes.Buffer
	cmd.Stderr = &hostStderr

	log.Printf("▶ starting upterm session: upterm %s", strings.Join(hostArgs, " "))

	if err := cmd.Start(); err != nil {
		cancel()
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to start upterm host: %v", err))
		return
	}

	// Store cancel func so the session can be cleaned up later if needed
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("upterm host exited: %v (stderr: %s)", err, hostStderr.String())
		}
		cancel()
	}()

	// Wait for the session to be established by polling upterm session current
	var sessionJSON map[string]any
	established := false

	for i := 0; i < 30; i++ { // retry for up to 15 seconds
		time.Sleep(500 * time.Millisecond)

		sessionJSON, err = getSessionInfo(uptermPath)
		if err == nil && sessionJSON != nil {
			established = true
			break
		}
		log.Printf("  waiting for upterm session... attempt %d/30", i+1)
	}

	if !established {
		// Kill the background process if session never came up
		cancel()
		errMsg := "upterm session did not start within timeout"
		if hostStderr.Len() > 0 {
			errMsg += ": " + hostStderr.String()
		}
		writeError(w, http.StatusGatewayTimeout, errMsg)
		return
	}

	// Build response
	resp := ShellResponse{
		Success: true,
		Raw:     sessionJSON,
	}

	if v, ok := sessionJSON["sessionId"]; ok {
		resp.SessionID = fmt.Sprintf("%v", v)
	}
	if v, ok := sessionJSON["host"]; ok {
		resp.Host = fmt.Sprintf("%v", v)
	}
	if v, ok := sessionJSON["command"]; ok {
		resp.Command = fmt.Sprintf("%v", v)
	}

	// Build the SSH command string from session info
	resp.SSH = buildSSHCommand(sessionJSON)

	log.Printf("✓ upterm session created: %s", resp.SessionID)
	json.NewEncoder(w).Encode(resp)
}

// getSessionInfo queries the current upterm session and returns parsed JSON.
func getSessionInfo(uptermPath string) (map[string]any, error) {
	cmd := exec.Command(uptermPath, "session", "current", "-o", "json")
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("session query failed: %v (stderr: %s)", err, stderr.String())
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return nil, fmt.Errorf("empty session output")
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to parse session JSON: %v (raw: %s)", err, output)
	}

	return result, nil
}

// buildSSHCommand constructs the SSH connection string from session data.
func buildSSHCommand(session map[string]any) string {
	host, _ := session["host"].(string)
	sessionID, _ := session["sessionId"].(string)

	if host == "" || sessionID == "" {
		return ""
	}

	// Parse the host URL to extract hostname
	// upterm returns host like "ssh://uptermd.upterm.dev:22"
	hostClean := strings.TrimPrefix(host, "ssh://")
	hostClean = strings.TrimPrefix(hostClean, "wss://")
	hostClean = strings.TrimPrefix(hostClean, "ws://")

	// Remove port if default SSH port
	if strings.HasSuffix(hostClean, ":22") {
		hostClean = strings.TrimSuffix(hostClean, ":22")
	}

	return fmt.Sprintf("ssh %s@%s", sessionID, hostClean)
}

// writeError sends a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ShellResponse{
		Success: false,
		Error:   msg,
	})
}
