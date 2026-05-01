package capabilities

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// RepairInfra stops the infrastructure (docker compose down), restarts the
// docker service, and then executes ./setup.sh update.
func RepairInfra(defaultInstallDir string) Handler {
	return func(ctx context.Context, params map[string]any) (string, error) {
		dir := defaultInstallDir
		if v, ok := params["install_dir"].(string); ok && v != "" {
			dir = v
		}
		if dir == "" {
			return "", fmt.Errorf("install_dir is not configured")
		}

		var fullOutput bytes.Buffer

		// 1. docker compose down
		logOutput(&fullOutput, "Stopping infrastructure (docker compose down)...")
		cmdDown := exec.CommandContext(ctx, "docker", "compose", "down")
		cmdDown.Dir = dir
		cmdDown.Stdout = &fullOutput
		cmdDown.Stderr = &fullOutput
		if err := cmdDown.Run(); err != nil {
			// We continue even if down fails, as the goal is to repair/restart
			fmt.Fprintf(&fullOutput, "Warning: docker compose down failed: %v\n", err)
		}

		// 2. sudo systemctl restart docker
		logOutput(&fullOutput, "Restarting Docker service (sudo systemctl restart docker)...")
		cmdRestart := exec.CommandContext(ctx, "sudo", "systemctl", "restart", "docker")
		cmdRestart.Stdout = &fullOutput
		cmdRestart.Stderr = &fullOutput
		if err := cmdRestart.Run(); err != nil {
			return fullOutput.String(), fmt.Errorf("failed to restart docker: %w", err)
		}

		// 3. ./setup.sh update
		logOutput(&fullOutput, "Executing update (./setup.sh update)...")
		script := filepath.Join(dir, "setup.sh")
		cmdUpdate := exec.CommandContext(ctx, "bash", script, "update")
		cmdUpdate.Dir = dir
		cmdUpdate.Stdout = &fullOutput
		cmdUpdate.Stderr = &fullOutput
		if err := cmdUpdate.Run(); err != nil {
			return fullOutput.String(), fmt.Errorf("setup.sh update failed: %w", err)
		}

		return strings.TrimSpace(fullOutput.String()), nil
	}
}

func logOutput(w *bytes.Buffer, msg string) {
	fmt.Fprintf(w, "\n--- %s ---\n", msg)
}
