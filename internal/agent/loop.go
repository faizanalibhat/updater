package agent

import (
	"context"
	"log"
	"strings"
	"time"

	"snapsec-agent/internal/capabilities"
	"snapsec-agent/internal/config"
)

// Run is the agent's main loop: register (if needed), then heartbeat
// forever, executing actions and self-updating as instructed.
func Run(ctx context.Context, cfg *config.Config, reg *capabilities.Registry, version string) error {
	client := New(cfg)

	caps := reg.Names()
	capCSV := strings.Join(caps, ",")

	// 1) Ensure registered.
	for {
		err := client.EnsureRegistered(ctx, caps, version)
		if err == nil {
			break
		}
		log.Printf("registration failed: %v (retrying in 30s)", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}
	log.Printf("✓ registered as agent_id=%s", cfg.AgentID)

	// 2) Heartbeat loop.
	var pendingResults []capabilities.Result
	interval := time.Duration(cfg.HeartbeatIntervalSeconds) * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		hbCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		resp, err := client.Heartbeat(hbCtx, capCSV, version, pendingResults)
		cancel()

		if err != nil {
			log.Printf("heartbeat failed: %v", err)
		} else {
			pendingResults = pendingResults[:0]

			// Honour server-side interval override.
			if resp.HeartbeatIntervalSeconds > 0 {
				newInterval := time.Duration(resp.HeartbeatIntervalSeconds) * time.Second
				if newInterval != interval {
					interval = newInterval
					cfg.HeartbeatIntervalSeconds = resp.HeartbeatIntervalSeconds
					_ = cfg.Save()
				}
			}

			// Self-update takes priority — if a newer version is available
			// we replace the binary and restart, dropping any pending actions
			// (the new agent will receive them on its next heartbeat).
			if resp.LatestVersion != "" && resp.LatestVersion != version && resp.DownloadURL != "" {
				log.Printf("self-update: %s -> %s", version, resp.LatestVersion)
				if err := SelfUpdate(ctx, resp.DownloadURL, resp.DownloadSHA256); err != nil {
					log.Printf("self-update failed: %v", err)
				} else {
					_ = cfg.SetVersion(resp.LatestVersion)
					log.Printf("self-update staged; awaiting systemd restart")
					// Don't execute actions in this cycle; we're going down.
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(interval):
					}
					continue
				}
			}

			// Execute actions sequentially.
			for _, a := range resp.Actions {
				if !reg.Has(a.Type) {
					pendingResults = append(pendingResults, capabilities.Result{
						ActionID:    a.ID,
						Capability:  a.Type,
						StartedAt:   time.Now().UTC(),
						CompletedAt: time.Now().UTC(),
						Error:       "capability not supported by this agent",
					})
					log.Printf("✗ action %s: capability %q not supported", a.ID, a.Type)
					continue
				}
				actionCtx, ac := context.WithTimeout(ctx, 30*time.Minute)
				res := reg.Execute(actionCtx, a)
				ac()
				pendingResults = append(pendingResults, res)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
