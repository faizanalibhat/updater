package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"updater/handlers"
)

type depCheck struct {
	Name    string
	Cmd     string
	Args    []string
	Purpose string
}

func checkDependencies() {
	deps := []depCheck{
		{"docker", "docker", []string{"--version"}, "/api/update"},
		{"docker compose", "docker", []string{"compose", "version"}, "/api/update"},
		{"upterm", "upterm", []string{"version"}, "/api/shell"},
	}

	var missing []string
	for _, d := range deps {
		cmd := exec.Command(d.Cmd, d.Args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			missing = append(missing, fmt.Sprintf("  ❌ %-18s (needed for %s)", d.Name, d.Purpose))
		} else {
			ver := strings.TrimSpace(strings.Split(string(out), "\n")[0])
			log.Printf("  ✅ %-18s %s", d.Name, ver)
		}
	}

	if len(missing) > 0 {
		log.Println("\n⚠️  Missing dependencies (endpoints will fail without them):")
		for _, m := range missing {
			log.Println(m)
		}
		log.Println()
	}
}

const serviceTemplate = `[Unit]
Description=Updater Daemon (Docker update & shell access)
After=network.target docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=%s
WorkingDirectory=%s
Restart=always
RestartSec=5
Environment=UPDATER_PORT=%s
Environment=COMPOSE_WORK_DIR=%s
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/local/go/bin
StandardOutput=journal
StandardError=journal
SyslogIdentifier=updater

[Install]
WantedBy=multi-user.target
`

func installService() {
	binPath, err := os.Executable()
	if err != nil {
		log.Fatalf("failed to resolve binary path: %v", err)
	}
	workDir := filepath.Dir(binPath)

	port := os.Getenv("UPDATER_PORT")
	if port == "" {
		port = "9876"
	}

	composeDir := os.Getenv("COMPOSE_WORK_DIR")
	if composeDir == "" {
		// Check for --compose-dir flag
		for i, arg := range os.Args {
			if arg == "--compose-dir" && i+1 < len(os.Args) {
				composeDir = os.Args[i+1]
				break
			}
			if strings.HasPrefix(arg, "--compose-dir=") {
				composeDir = strings.TrimPrefix(arg, "--compose-dir=")
				break
			}
		}
	}
	if composeDir == "" {
		composeDir = workDir // fallback to binary dir
	}

	unit := fmt.Sprintf(serviceTemplate, binPath, workDir, port, composeDir)

	if err := os.WriteFile("/etc/systemd/system/updater.service", []byte(unit), 0644); err != nil {
		log.Fatalf("failed to write service file (run with sudo): %v", err)
	}

	for _, args := range [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "updater.service"},
		{"systemctl", "restart", "updater.service"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("failed to run %s: %v", strings.Join(args, " "), err)
		}
	}

	log.Println("✅ updater.service installed, enabled, and started")
	log.Println("   systemctl status updater    — check status")
	log.Println("   journalctl -u updater -f    — follow logs")
}

func uninstallService() {
	for _, args := range [][]string{
		{"systemctl", "stop", "updater.service"},
		{"systemctl", "disable", "updater.service"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run() // ignore errors, service may not exist
	}

	os.Remove("/etc/systemd/system/updater.service")
	exec.Command("systemctl", "daemon-reload").Run()

	log.Println("✅ updater.service removed")
}

func printUsage() {
	fmt.Println(`Usage: updater [command]

Commands:
  (none)        Start the server in foreground
  --install     Install and start as a systemd service (requires sudo)
                  --compose-dir=<path>  Set the docker-compose working directory
  --uninstall   Stop and remove the systemd service (requires sudo)
  --status      Show the systemd service status`)
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--install":
			installService()
			return
		case "--uninstall":
			uninstallService()
			return
		case "--status":
			cmd := exec.Command("systemctl", "status", "updater.service")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Run()
			return
		case "--help", "-h":
			printUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
			printUsage()
			os.Exit(1)
		}
	}

	port := os.Getenv("UPDATER_PORT")
	if port == "" {
		port = "9876"
	}

	log.Println("Checking dependencies...")
	checkDependencies()

	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"time":   time.Now().Format(time.RFC3339),
		})
	})

	// Docker update and cleanup
	mux.HandleFunc("POST /api/update", handlers.HandleUpdate)

	// Shell session via upterm
	mux.HandleFunc("POST /api/shell", handlers.HandleShell)

	// Bind to localhost only — do NOT expose externally
	addr := fmt.Sprintf("127.0.0.1:%s", port)
	log.Printf("🚀 Updater service listening on %s", addr)

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // long timeout for docker pull operations
		IdleTimeout:  60 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
