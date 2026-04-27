package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SelfUpdate replaces the current binary with the one at downloadURL and
// restarts the systemd unit so the new version is picked up.
//
// expectedSHA256 may be empty to skip verification.
func SelfUpdate(ctx context.Context, downloadURL, expectedSHA256 string) error {
	if downloadURL == "" {
		return fmt.Errorf("download_url is empty")
	}

	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}
	// Resolve any symlinks so we replace the real file.
	if real, err := filepath.EvalSymlinks(binPath); err == nil {
		binPath = real
	}

	tmp := binPath + ".new"

	// Download.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download http %d", resp.StatusCode)
	}

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hasher), resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	// Verify checksum if provided.
	if expectedSHA256 != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(got, expectedSHA256) {
			os.Remove(tmp)
			return fmt.Errorf("sha256 mismatch: got %s want %s", got, expectedSHA256)
		}
	}

	// Atomic rename onto the running binary (Linux allows this — the running
	// process keeps executing the unlinked inode).
	if err := os.Rename(tmp, binPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}

	// Restart the systemd unit so the new binary takes over. We exec
	// systemctl in the background; systemd will SIGTERM us shortly after.
	cmd := exec.Command("systemctl", "restart", "snapsec-agent.service")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	return nil
}
