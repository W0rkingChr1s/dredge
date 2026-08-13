// Package report renders human-readable summaries of plans and results.
package report

import (
	"fmt"
	"strings"

	"github.com/W0rkingChr1s/dredge/internal/janitor"
)

// FmtSize renders a byte count as a human-readable size.
func FmtSize(bytes int64) string {
	if bytes <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	n := float64(bytes)
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", bytes, units[i])
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

const sampleLimit = 15

// OverviewHTML builds a Telegram-friendly (parse_mode=HTML) overview of a plan.
func OverviewHTML(host string, plan *janitor.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>🐳 Docker-Cleanup — %s</b>\n", esc(host))
	fmt.Fprintf(&b, "<i>Übersicht bereinigbarer Ressourcen</i>\n\n")

	for _, g := range plan.Groups {
		rem := g.Removable()
		fmt.Fprintf(&b, "<b>%s (%d)</b> — %s\n", esc(g.Label), len(rem), FmtSize(g.RemovableSize()))
		if len(rem) == 0 {
			b.WriteString("– keine –\n")
		} else {
			for i, c := range rem {
				if i >= sampleLimit {
					fmt.Fprintf(&b, "… und %d weitere\n", len(rem)-sampleLimit)
					break
				}
				if c.Size > 0 {
					fmt.Fprintf(&b, "• <code>%s</code> — %s\n", esc(trunc(c.Name, 48)), FmtSize(c.Size))
				} else {
					fmt.Fprintf(&b, "• <code>%s</code>\n", esc(trunc(c.Name, 48)))
				}
			}
		}
		if prot := g.Protected(); len(prot) > 0 {
			fmt.Fprintf(&b, "<i>🛡 %d geschützt</i>\n", len(prot))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "<b>Gesamt bereinigbar: %s in %d Objekten</b>",
		FmtSize(plan.TotalRemovableSize()), plan.TotalRemovableCount())
	return b.String()
}

// OverviewPlain is a plain-text version for the terminal / non-HTML channels.
func OverviewPlain(host string, plan *janitor.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Docker-Cleanup — %s\n\n", host)
	for _, g := range plan.Groups {
		rem := g.Removable()
		fmt.Fprintf(&b, "%-22s %3d Objekte  %10s\n", g.Label, len(rem), FmtSize(g.RemovableSize()))
	}
	fmt.Fprintf(&b, "\nGesamt bereinigbar: %s in %d Objekten\n",
		FmtSize(plan.TotalRemovableSize()), plan.TotalRemovableCount())
	return b.String()
}

// ConfirmationHTML renders the post-run confirmation for Telegram.
func ConfirmationHTML(res *janitor.Result) string {
	var b strings.Builder
	if res.DryRun {
		b.WriteString("<b>🧪 Docker-Cleanup (Probelauf) — nichts wurde gelöscht</b>\n")
	} else {
		b.WriteString("<b>✅ Docker-Cleanup abgeschlossen</b>\n")
	}
	for _, t := range res.Types {
		if t.Removed == 0 && t.Failed == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s: %d entfernt (%s)", esc(t.Label), t.Removed, FmtSize(t.Freed))
		if t.Failed > 0 {
			fmt.Fprintf(&b, ", %d Fehler", t.Failed)
		}
		b.WriteString("\n")
	}
	verb := "freigegeben"
	if res.DryRun {
		verb = "freigebbar (simuliert)"
	}
	fmt.Fprintf(&b, "<b>Gesamt %s: %s</b>", verb, FmtSize(res.TotalFreed()))
	if res.TotalFailed() > 0 {
		fmt.Fprintf(&b, "\n⚠️ %d Objekte konnten nicht entfernt werden.", res.TotalFailed())
	}
	return b.String()
}

// ConfirmationPlain renders the confirmation as plain text.
func ConfirmationPlain(res *janitor.Result) string {
	var b strings.Builder
	if res.DryRun {
		b.WriteString("Docker-Cleanup (Probelauf) — nichts gelöscht\n")
	} else {
		b.WriteString("Docker-Cleanup abgeschlossen\n")
	}
	for _, t := range res.Types {
		if t.Removed == 0 && t.Failed == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %-22s %3d entfernt  %10s", t.Label, t.Removed, FmtSize(t.Freed))
		if t.Failed > 0 {
			fmt.Fprintf(&b, "  (%d Fehler)", t.Failed)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Gesamt freigegeben: %s\n", FmtSize(res.TotalFreed()))
	return b.String()
}

func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
