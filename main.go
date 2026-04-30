package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"snapsec-agent/internal/agent"
	"snapsec-agent/internal/capabilities"
	"snapsec-agent/internal/config"
)

// version is overridden at build time via -ldflags="-X main.version=..."
var version = "dev"

const serviceName = "snapsec-agent.service"

const serviceTemplate = `[Unit]
Description=Snapsec on-prem management agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=5
Environment=SNAPSEC_AGENT_CONFIG=%s
Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
StandardOutput=journal
StandardError=journal
SyslogIdentifier=snapsec-agent

[Install]
WantedBy=multi-user.target
`

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	var (
		fInstall        = flag.Bool("install", false, "install and start the systemd service (requires sudo)")
		fUninstall      = flag.Bool("uninstall", false, "stop and remove the systemd service (requires sudo)")
		fStatus         = flag.Bool("status", false, "show systemd service status")
		fConfig         = flag.String("config", "", "path to config.yaml (overrides $SNAPSEC_AGENT_CONFIG and the default)")
		fAdminURL       = flag.String("admin-url", "", "(install) admin server base URL, e.g. https://admin.snapsec.co")
		fBaseURL        = flag.String("base-url", "", "(install) public-facing URL where this instance is hosted (matches BASE_URL in the product .env)")
		fEnroll         = flag.String("enrollment-token", "", "(install) one-time enrollment token issued by the admin panel")
		fInstallDir     = flag.String("install-dir", "", "(install) product install directory containing setup.sh and .env")
		fCFAccessID     = flag.String("cf-access-id", "", "(install) Cloudflare Access service token client id")
		fCFAccessSecret = flag.String("cf-access-secret", "", "(install) Cloudflare Access service token client secret")
		fVersion        = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *fVersion {
		fmt.Println(version)
		return
	}

	switch {
	case *fInstall:
		installService(*fConfig, *fAdminURL, *fBaseURL, *fEnroll, *fInstallDir, *fCFAccessID, *fCFAccessSecret)
		return
	case *fUninstall:
		uninstallService()
		return
	case *fStatus:
		cmd := exec.Command("systemctl", "status", serviceName)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
		return
	}

	// Foreground / service mode.
	cfgPath := *fConfig
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.AdminURL == "" {
		log.Fatalf("config %s: admin_url is empty (re-run with --install or edit the file)", cfgPath)
	}

	reg := capabilities.NewRegistry()
	reg.Register("update_application", capabilities.UpdateApplication(cfg.InstallDir))
	reg.Register("set_license_expiry", capabilities.SetLicenseExpiry(cfg.MongoConnection))

	log.Printf("snapsec-agent version=%s config=%s admin=%s", version, cfg.Path(), cfg.AdminURL)
	log.Printf("registered capabilities: %s", strings.Join(reg.Names(), ", "))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := agent.Run(ctx, cfg, reg, version); err != nil && err != context.Canceled {
		log.Fatalf("agent: %v", err)
	}
}

// ---- service management ---------------------------------------------------

func installService(cfgPath, adminURL, baseURL, enrollment, installDir, cfID, cfSecret string) {
	binPath, err := os.Executable()
	if err != nil {
		log.Fatalf("resolve binary path: %v", err)
	}
	if real, err := filepath.EvalSymlinks(binPath); err == nil {
		binPath = real
	}

	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if adminURL != "" {
		cfg.AdminURL = adminURL
	}
	if baseURL != "" {
		cfg.BaseURL = baseURL
	}
	if enrollment != "" {
		cfg.EnrollmentToken = enrollment
	}
	if installDir != "" {
		cfg.InstallDir = installDir
	}
	if cfID != "" {
		cfg.CFAccessClientID = cfID
	}
	if cfSecret != "" {
		cfg.CFAccessClientSecret = cfSecret
	}
	if cfg.CurrentVersion == "" {
		cfg.CurrentVersion = version
	}
	if err := cfg.Save(); err != nil {
		log.Fatalf("save config: %v", err)
	}

	unit := fmt.Sprintf(serviceTemplate, binPath, cfgPath)
	unitPath := "/etc/systemd/system/" + serviceName
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		log.Fatalf("write %s (run with sudo): %v", unitPath, err)
	}

	for _, args := range [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", serviceName},
		{"systemctl", "restart", serviceName},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("%s: %v", strings.Join(args, " "), err)
		}
	}

	log.Printf("✅ %s installed (config=%s)", serviceName, cfgPath)
	log.Printf("   systemctl status snapsec-agent")
	log.Printf("   journalctl -u snapsec-agent -f")
}

func uninstallService() {
	for _, args := range [][]string{
		{"systemctl", "stop", serviceName},
		{"systemctl", "disable", serviceName},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}
	_ = os.Remove("/etc/systemd/system/" + serviceName)
	_ = exec.Command("systemctl", "daemon-reload").Run()
	log.Printf("✅ %s removed", serviceName)
}
