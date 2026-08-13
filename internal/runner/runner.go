// Package runner orchestrates a full cleanup run: scan, notify/approve,
// execute, confirm and log.
package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/W0rkingChr1s/dredge/internal/audit"
	"github.com/W0rkingChr1s/dredge/internal/config"
	"github.com/W0rkingChr1s/dredge/internal/docker"
	"github.com/W0rkingChr1s/dredge/internal/janitor"
	"github.com/W0rkingChr1s/dredge/internal/notify"
	"github.com/W0rkingChr1s/dredge/internal/report"
)

// Options tweak a run.
type Options struct {
	ForceDryRun bool             // override config: never delete
	Yes         bool             // non-interactive: approve all ask types (CLI)
	Log         func(msg string) // progress callback (may be nil)
}

func (o Options) log(format string, a ...any) {
	if o.Log != nil {
		o.Log(fmt.Sprintf(format, a...))
	}
}

// Run performs a full cleanup cycle. It returns the plan, the result and an
// error only for hard failures (connectivity, etc.).
func Run(ctx context.Context, cfg *config.Config, opts Options) (*janitor.Plan, *janitor.Result, error) {
	start := time.Now()
	if opts.ForceDryRun {
		cfg.Safety.DryRun = true
	}

	cli, err := docker.New(cfg.Docker.Host, cfg.Docker.APIVersion,
		time.Duration(maxInt(cfg.Docker.TimeoutSeconds, 10))*time.Second)
	if err != nil {
		return nil, nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := cli.Ping(pingCtx); err != nil {
		return nil, nil, fmt.Errorf("Docker-Host nicht erreichbar (%s): %w", cfg.Docker.Host, err)
	}

	opts.log("Scanne %s …", cfg.Docker.Host)
	plan, err := janitor.Scan(ctx, cli, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("Scan fehlgeschlagen: %w", err)
	}
	opts.log("Bereinigbar: %s in %d Objekten",
		report.FmtSize(plan.TotalRemovableSize()), plan.TotalRemovableCount())

	// Trigger gates.
	if !plan.HasAnything() {
		opts.log("Nichts zu tun.")
		if cfg.Notify.ReportOnNothing {
			notifyAll(cfg, "Docker-Cleanup", "✅ Nichts zu bereinigen auf "+cfg.Docker.Host)
		}
		return plan, &janitor.Result{DryRun: cfg.Safety.DryRun}, nil
	}
	if cfg.Trigger.MinReclaimableMB > 0 {
		if plan.TotalRemovableSize() < cfg.Trigger.MinReclaimableMB*1024*1024 {
			opts.log("Unter Schwellwert (%d MB) — übersprungen.", cfg.Trigger.MinReclaimableMB)
			return plan, &janitor.Result{DryRun: cfg.Safety.DryRun}, nil
		}
	}

	// Build decisions: auto types are pre-approved.
	decisions := janitor.DecisionsForAuto(cfg)
	askTypes := janitor.AskTypes(cfg, plan)

	if opts.Yes {
		// CLI --yes: approve everything enabled that has candidates.
		for _, t := range askTypes {
			decisions[t] = true
		}
	} else if len(askTypes) > 0 {
		if cfg.Notify.Telegram.Enabled && cfg.Notify.Telegram.BotToken != "" {
			if err := interactiveApprove(ctx, cfg, plan, askTypes, decisions, opts); err != nil {
				opts.log("Freigabe-Fehler: %v", err)
			}
		} else {
			opts.log("Ask-Typen vorhanden, aber kein interaktiver Kanal — diese werden übersprungen.")
			notifyAll(cfg, "Docker-Cleanup", report.OverviewPlain(cfg.Docker.Host, plan)+
				"\n(Interaktive Freigabe nicht konfiguriert — nur Auto-Typen wurden bereinigt.)")
		}
	}

	// Execute.
	anyApproved := false
	for _, v := range decisions {
		if v {
			anyApproved = true
			break
		}
	}
	res := &janitor.Result{DryRun: cfg.Safety.DryRun}
	if anyApproved {
		opts.log("Führe Bereinigung aus …")
		res = janitor.Execute(ctx, cli, plan, cfg, decisions)
		opts.log("Fertig: %s freigegeben, %d entfernt, %d Fehler",
			report.FmtSize(res.TotalFreed()), res.TotalRemoved(), res.TotalFailed())
	} else {
		opts.log("Keine Freigabe — nichts bereinigt.")
	}

	// Confirmation.
	sendConfirmation(ctx, cfg, res)

	// Audit.
	mode := "auto"
	if cfg.Safety.DryRun {
		mode = "dry-run"
	} else if len(askTypes) > 0 {
		mode = "interactive"
	}
	_ = audit.Append(cfg.Audit.LogFile, cfg.Docker.Host, mode, res, time.Since(start))

	return plan, res, nil
}

func interactiveApprove(ctx context.Context, cfg *config.Config, plan *janitor.Plan, askTypes []string, decisions map[string]bool, opts Options) error {
	bot := notify.NewBot(cfg.Notify.Telegram.BotToken, cfg.Notify.Telegram.ChatID)

	text := report.OverviewHTML(cfg.Docker.Host, plan)
	if autoNote := autoTypesNote(cfg, plan); autoNote != "" {
		text += "\n\n<i>" + autoNote + "</i>"
	}
	text += "\n\n<b>Was soll zusätzlich bereinigt werden?</b>"

	rows, allowed := buildButtons(cfg, plan, askTypes)
	msgID, err := bot.SendButtons(ctx, text, rows)
	if err != nil {
		return err
	}

	timeout := time.Duration(maxInt(cfg.Notify.Telegram.ApprovalTimeoutMinutes, 1)) * time.Minute
	opts.log("Warte auf Telegram-Freigabe (max %s) …", timeout)
	choice, err := bot.AwaitCallback(ctx, msgID, allowed, timeout)
	if err != nil {
		if _, ok := err.(notify.ErrTimeout); ok {
			applyTimeoutDefault(cfg, askTypes, decisions)
			opts.log("Timeout — Default-Aktion '%s' angewendet.", cfg.Notify.Telegram.OnTimeout)
			_, _ = bot.SendHTML(ctx, fmt.Sprintf("⏰ Keine Antwort — Default-Aktion <b>%s</b> angewendet.",
				defaultLabel(cfg.Notify.Telegram.OnTimeout)))
			return nil
		}
		return err
	}
	applyChoice(choice, askTypes, decisions)
	return nil
}

func buildButtons(cfg *config.Config, plan *janitor.Plan, askTypes []string) ([][]notify.Button, []string) {
	var rows [][]notify.Button
	allowed := []string{"all", "none"}
	rows = append(rows, []notify.Button{{Text: "🧹 Alles bereinigen", Data: "all"}})
	if len(askTypes) > 1 {
		var row []notify.Button
		for _, t := range askTypes {
			g := plan.Group(t)
			label := t
			if g != nil {
				label = g.Label
			}
			data := "only:" + t
			allowed = append(allowed, data)
			row = append(row, notify.Button{Text: "▶ " + short(label), Data: data})
			if len(row) == 2 {
				rows = append(rows, row)
				row = nil
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	rows = append(rows, []notify.Button{{Text: "✋ Nichts", Data: "none"}})
	return rows, allowed
}

func applyChoice(choice string, askTypes []string, decisions map[string]bool) {
	switch {
	case choice == "all":
		for _, t := range askTypes {
			decisions[t] = true
		}
	case choice == "none":
		// leave ask types false
	case strings.HasPrefix(choice, "only:"):
		t := strings.TrimPrefix(choice, "only:")
		decisions[t] = true
	}
}

func applyTimeoutDefault(cfg *config.Config, askTypes []string, decisions map[string]bool) {
	switch cfg.Notify.Telegram.OnTimeout {
	case "all":
		for _, t := range askTypes {
			decisions[t] = true
		}
	case "dangling":
		for _, t := range askTypes {
			if t == janitor.TypeImagesDangling {
				decisions[t] = true
			}
		}
	default: // "none"
	}
}

func defaultLabel(onTimeout string) string {
	switch onTimeout {
	case "all":
		return "alles bereinigen"
	case "dangling":
		return "nur Dangling Images"
	default:
		return "nichts"
	}
}

func autoTypesNote(cfg *config.Config, plan *janitor.Plan) string {
	var parts []string
	for _, r := range cfg.Resources.All() {
		if r.Rule.Enabled && r.Rule.Mode == config.ModeAuto {
			if g := plan.Group(r.Key); g != nil && g.RemovableCount() > 0 {
				parts = append(parts, fmt.Sprintf("%s (%d)", r.Label, g.RemovableCount()))
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Automatisch bereinigt: " + strings.Join(parts, ", ")
}

func sendConfirmation(ctx context.Context, cfg *config.Config, res *janitor.Result) {
	if res.TotalRemoved() == 0 && res.TotalFailed() == 0 && !cfg.Notify.ReportOnNothing {
		return
	}
	if cfg.Notify.Telegram.Enabled && cfg.Notify.Telegram.BotToken != "" {
		bot := notify.NewBot(cfg.Notify.Telegram.BotToken, cfg.Notify.Telegram.ChatID)
		_, _ = bot.SendHTML(ctx, report.ConfirmationHTML(res))
	}
	if len(cfg.Notify.URLs) > 0 {
		notifyAll(cfg, "Docker-Cleanup abgeschlossen", report.ConfirmationPlain(res))
	}
}

func notifyAll(cfg *config.Config, title, message string) {
	if len(cfg.Notify.URLs) == 0 {
		return
	}
	_ = notify.SendAll(cfg.Notify.URLs, title, message)
}

func short(s string) string {
	if len(s) <= 18 {
		return s
	}
	return s[:17] + "…"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
