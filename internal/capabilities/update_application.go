package capabilities

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// UpdateApplication invokes `./setup.sh update` from the configured install
// directory. The optional "install_dir" param overrides the default.
func UpdateApplication(defaultInstallDir string) Handler {
	return func(ctx context.Context, params map[string]any) (string, error) {
		dir := defaultInstallDir
		if v, ok := params["install_dir"].(string); ok && v != "" {
			dir = v
		}
		if dir == "" {
			return "", fmt.Errorf("install_dir is not configured")
		}

		script := filepath.Join(dir, "setup.sh")
		cmd := exec.CommandContext(ctx, "bash", script, "update")
		cmd.Dir = dir

		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out

		if err := cmd.Run(); err != nil {
			return strings.TrimSpace(out.String()), fmt.Errorf("setup.sh update failed: %w", err)
		}
		return strings.TrimSpace(out.String()), nil
	}
}
