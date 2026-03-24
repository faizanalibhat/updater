package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// UpdateRequest holds optional parameters for the update endpoint.
type UpdateRequest struct {
	// Services to update. If empty, all services are updated.
	Services []string `json:"services,omitempty"`
	// ComposeFile overrides the default docker-compose file path.
	ComposeFile string `json:"composeFile,omitempty"`
	// WorkDir is the directory where docker compose commands run.
	WorkDir string `json:"workDir,omitempty"`
}

// UpdateResponse contains the result of the update operation.
type UpdateResponse struct {
	Success bool         `json:"success"`
	Steps   []StepResult `json:"steps"`
	Error   string       `json:"error,omitempty"`
}

// StepResult represents the outcome of a single step in the update process.
type StepResult struct {
	Name     string `json:"name"`
	Command  string `json:"command"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
	Duration string `json:"duration"`
}

// HandleUpdate performs a docker compose pull, up, and system prune.
func HandleUpdate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req UpdateRequest
	if r.Body != nil {
		defer r.Body.Close()
		json.NewDecoder(r.Body).Decode(&req) // ignore decode errors; defaults are fine
	}

	workDir := req.WorkDir
	if workDir == "" {
		workDir = os.Getenv("COMPOSE_WORK_DIR")
	}
	if workDir == "" {
		workDir = "/root/staging" // default production path
	}

	composeFile := req.ComposeFile
	if composeFile == "" {
		composeFile = os.Getenv("COMPOSE_FILE")
	}

	var steps []StepResult
	allSuccess := true

	// --- Step 1: docker compose pull ---
	pullArgs := buildComposeCommand(composeFile, "pull", req.Services)
	step := runCommand("Docker Compose Pull", workDir, pullArgs[0], pullArgs[1:]...)
	steps = append(steps, step)
	if step.Error != "" {
		allSuccess = false
	}

	// --- Step 2: docker compose up -d ---
	upArgs := buildComposeCommand(composeFile, "up -d", req.Services)
	step = runCommand("Docker Compose Up", workDir, upArgs[0], upArgs[1:]...)
	steps = append(steps, step)
	if step.Error != "" {
		allSuccess = false
	}

	// --- Step 3: docker system prune (cleanup dangling images/containers) ---
	step = runCommand("Docker System Prune", workDir, "docker", "system", "prune", "-f", "--volumes")
	steps = append(steps, step)
	if step.Error != "" {
		// prune failure is non-critical, don't fail the whole operation
		log.Printf("⚠️  prune warning: %s", step.Error)
	}

	resp := UpdateResponse{
		Success: allSuccess,
		Steps:   steps,
	}
	if !allSuccess {
		resp.Error = "one or more steps failed"
		w.WriteHeader(http.StatusInternalServerError)
	}

	json.NewEncoder(w).Encode(resp)
}

// buildComposeCommand constructs the docker compose command arguments.
func buildComposeCommand(composeFile, action string, services []string) []string {
	args := []string{"docker", "compose"}

	if composeFile != "" {
		args = append(args, "-f", composeFile)
	}

	args = append(args, strings.Fields(action)...)

	if len(services) > 0 {
		args = append(args, services...)
	}

	return args
}

// runCommand executes a shell command and returns a StepResult.
func runCommand(name, workDir, bin string, args ...string) StepResult {
	start := time.Now()

	cmd := exec.Command(bin, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	fullCmd := fmt.Sprintf("%s %s", bin, strings.Join(args, " "))
	log.Printf("▶ [%s] running: %s (dir: %s)", name, fullCmd, workDir)

	err := cmd.Run()
	duration := time.Since(start)

	result := StepResult{
		Name:     name,
		Command:  fullCmd,
		Output:   strings.TrimSpace(stdout.String() + "\n" + stderr.String()),
		Duration: duration.Round(time.Millisecond).String(),
	}

	if err != nil {
		result.Error = err.Error()
		log.Printf("✗ [%s] failed after %s: %v", name, result.Duration, err)
	} else {
		log.Printf("✓ [%s] completed in %s", name, result.Duration)
	}

	return result
}
