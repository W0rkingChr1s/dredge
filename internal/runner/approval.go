package runner

import (
	"fmt"
	"strings"

	"github.com/W0rkingChr1s/dredge/internal/janitor"
	"github.com/W0rkingChr1s/dredge/internal/notify"
	"github.com/W0rkingChr1s/dredge/internal/report"
)

// Callback-Daten der Buttons. Telegram erlaubt 64 Byte pro Button, die
// Typ-Schlüssel sind deutlich kürzer.
const (
	cbAll     = "all"
	cbNone    = "none"
	cbConfirm = "go"
	cbToggle  = "t:"
)

// approvalItem is one selectable resource type.
type approvalItem struct {
	Key   string
	Label string
	Size  int64
	Count int
}

// approval is the state machine behind the interactive Telegram dialog: one
// toggle per resource type plus the terminal actions. It holds no I/O so the
// whole button flow is testable without a bot.
type approval struct {
	items    []approvalItem
	selected map[string]bool
}

func newApproval(plan *janitor.Plan, askTypes []string) *approval {
	a := &approval{selected: make(map[string]bool, len(askTypes))}
	for _, t := range askTypes {
		it := approvalItem{Key: t, Label: t}
		if g := plan.Group(t); g != nil {
			it.Label = g.Label
			it.Size = g.RemovableSize()
			it.Count = g.RemovableCount()
		}
		a.items = append(a.items, it)
	}
	return a
}

// multi reports whether there is anything to pick from. With a single ask type
// the toggles would only add a click, so the dialog stays a simple yes/no.
func (a *approval) multi() bool { return len(a.items) > 1 }

// Keyboard renders the buttons for the current selection.
func (a *approval) Keyboard() [][]notify.Button {
	rows := [][]notify.Button{{{Text: "🧹 Alles bereinigen", Data: cbAll}}}
	if a.multi() {
		for _, it := range a.items {
			box := "☐"
			if a.selected[it.Key] {
				box = "☑"
			}
			// Volumes sind der einzige Typ, dessen Löschung Daten kostet –
			// das gehört auf den Button, nicht nur in den Text darüber.
			warn := ""
			if it.Key == janitor.TypeVolumes {
				warn = " ⚠"
			}
			// Eine Zeile pro Typ: auf dem Handy bleiben die Labels sonst
			// nicht lesbar, sobald die Checkbox davor steht.
			rows = append(rows, []notify.Button{{
				Text: fmt.Sprintf("%s %s%s · %s", box, it.Label, warn, report.FmtSize(it.Size)),
				Data: cbToggle + it.Key,
			}})
		}
		rows = append(rows, []notify.Button{{Text: a.confirmLabel(), Data: cbConfirm}})
	}
	rows = append(rows, []notify.Button{{Text: "✋ Nichts", Data: cbNone}})
	return rows
}

func (a *approval) confirmLabel() string {
	n, size := a.totals()
	if n == 0 {
		return "✅ Auswahl bereinigen"
	}
	return fmt.Sprintf("✅ Auswahl bereinigen · %d · %s", n, report.FmtSize(size))
}

// Press applies a button press and reports what the caller should do next.
func (a *approval) Press(data string) notify.CallbackAction {
	switch {
	case data == cbAll:
		for _, it := range a.items {
			a.selected[it.Key] = true
		}
		return notify.CallbackAction{Toast: "Alles freigegeben", Done: true}

	case data == cbNone:
		a.selected = map[string]bool{}
		return notify.CallbackAction{Toast: "Es wird nichts bereinigt", Done: true}

	case data == cbConfirm:
		if n, _ := a.totals(); n == 0 {
			// Kein Done: der Dialog bleibt offen, sonst wäre ein Fehlgriff
			// gleichbedeutend mit "nichts bereinigen".
			return notify.CallbackAction{
				Toast:    "Nichts ausgewählt – erst Typen antippen oder ✋ Nichts wählen.",
				Keyboard: a.Keyboard(),
			}
		}
		return notify.CallbackAction{Toast: "Auswahl übernommen", Done: true}

	case strings.HasPrefix(data, cbToggle):
		key := strings.TrimPrefix(data, cbToggle)
		it, ok := a.item(key)
		if !ok {
			return notify.CallbackAction{Toast: "Unbekannter Typ"}
		}
		a.selected[key] = !a.selected[key]
		mark := "abgewählt"
		if a.selected[key] {
			mark = "ausgewählt"
		}
		return notify.CallbackAction{
			Toast:    it.Label + " " + mark,
			Keyboard: a.Keyboard(),
		}
	}
	return notify.CallbackAction{Toast: "Ungültige Auswahl"}
}

// Chosen returns the selected type keys in the plan's order.
func (a *approval) Chosen() []string {
	var out []string
	for _, it := range a.items {
		if a.selected[it.Key] {
			out = append(out, it.Key)
		}
	}
	return out
}

// Summary describes the decision for the edited message.
func (a *approval) Summary() string {
	chosen := a.Chosen()
	if len(chosen) == 0 {
		return "Nichts freigegeben."
	}
	labels := make([]string, 0, len(chosen))
	for _, it := range a.items {
		if a.selected[it.Key] {
			labels = append(labels, it.Label)
		}
	}
	_, size := a.totals()
	return fmt.Sprintf("Freigegeben: %s (%s)", strings.Join(labels, ", "), report.FmtSize(size))
}

func (a *approval) totals() (count int, size int64) {
	for _, it := range a.items {
		if a.selected[it.Key] {
			count++
			size += it.Size
		}
	}
	return count, size
}

func (a *approval) item(key string) (approvalItem, bool) {
	for _, it := range a.items {
		if it.Key == key {
			return it, true
		}
	}
	return approvalItem{}, false
}
