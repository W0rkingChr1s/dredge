package runner

import (
	"strings"
	"testing"

	"github.com/W0rkingChr1s/dredge/internal/janitor"
	"github.com/W0rkingChr1s/dredge/internal/notify"
)

func testPlan() *janitor.Plan {
	mb := int64(1024 * 1024)
	return &janitor.Plan{Groups: []janitor.Group{
		{Type: janitor.TypeImagesDangling, Label: "Dangling Images", Candidates: []janitor.Candidate{
			{Type: janitor.TypeImagesDangling, ID: "a", Size: 400 * mb},
		}},
		{Type: janitor.TypeVolumes, Label: "Verwaiste Volumes", Candidates: []janitor.Candidate{
			{Type: janitor.TypeVolumes, ID: "v1", Size: 100 * mb},
			{Type: janitor.TypeVolumes, ID: "v2", Size: 100 * mb, Protected: true},
		}},
		{Type: janitor.TypeBuildCache, Label: "Build-Cache", Candidates: []janitor.Candidate{
			{Type: janitor.TypeBuildCache, ID: "c", Size: 50 * mb},
		}},
	}}
}

func newTestApproval() *approval {
	return newApproval(testPlan(), []string{
		janitor.TypeImagesDangling, janitor.TypeVolumes, janitor.TypeBuildCache,
	})
}

func TestApprovalMultiSelect(t *testing.T) {
	a := newTestApproval()

	// Zwei Typen antippen – der Dialog bleibt offen.
	for _, key := range []string{janitor.TypeImagesDangling, janitor.TypeBuildCache} {
		act := a.Press(cbToggle + key)
		if act.Done {
			t.Fatalf("Toggle %q darf den Dialog nicht beenden", key)
		}
		if act.Keyboard == nil {
			t.Errorf("Toggle %q muss die Tastatur aktualisieren", key)
		}
	}

	act := a.Press(cbConfirm)
	if !act.Done {
		t.Fatal("Bestätigen muss den Dialog beenden")
	}
	got := a.Chosen()
	want := []string{janitor.TypeImagesDangling, janitor.TypeBuildCache}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Chosen() = %v, want %v", got, want)
	}
}

func TestApprovalToggleOffAgain(t *testing.T) {
	a := newTestApproval()
	a.Press(cbToggle + janitor.TypeVolumes)
	a.Press(cbToggle + janitor.TypeVolumes)
	if got := a.Chosen(); len(got) != 0 {
		t.Errorf("zweimal antippen muss abwählen, Chosen() = %v", got)
	}
}

func TestApprovalConfirmWithoutSelectionKeepsDialogOpen(t *testing.T) {
	a := newTestApproval()
	act := a.Press(cbConfirm)
	if act.Done {
		t.Fatal("Bestätigen ohne Auswahl darf nicht als 'nichts bereinigen' durchgehen")
	}
	if !strings.Contains(act.Toast, "Nichts ausgewählt") {
		t.Errorf("Hinweis fehlt, Toast = %q", act.Toast)
	}
	// Danach lässt sich normal weiterarbeiten.
	a.Press(cbToggle + janitor.TypeVolumes)
	if act := a.Press(cbConfirm); !act.Done {
		t.Error("nach einer Auswahl muss Bestätigen greifen")
	}
}

func TestApprovalAllAndNone(t *testing.T) {
	a := newTestApproval()
	if act := a.Press(cbAll); !act.Done {
		t.Fatal("'Alles' muss sofort beenden")
	}
	if got := a.Chosen(); len(got) != 3 {
		t.Errorf("'Alles' muss alle Typen wählen, Chosen() = %v", got)
	}

	b := newTestApproval()
	b.Press(cbToggle + janitor.TypeVolumes)
	if act := b.Press(cbNone); !act.Done {
		t.Fatal("'Nichts' muss sofort beenden")
	}
	if got := b.Chosen(); len(got) != 0 {
		t.Errorf("'Nichts' muss eine bestehende Auswahl verwerfen, Chosen() = %v", got)
	}
}

func TestApprovalIgnoresUnknownData(t *testing.T) {
	a := newTestApproval()
	for _, data := range []string{"", "quatsch", cbToggle + "gibtsnicht", "only:volumes"} {
		act := a.Press(data)
		if act.Done {
			t.Errorf("unbekannte Callback-Daten %q dürfen nicht beenden", data)
		}
		if len(a.Chosen()) != 0 {
			t.Errorf("unbekannte Callback-Daten %q haben etwas ausgewählt", data)
		}
	}
}

func TestApprovalKeyboard(t *testing.T) {
	a := newTestApproval()

	rows := a.Keyboard()
	flat := flatten(rows)
	if !strings.Contains(flat, "☐ Dangling Images") {
		t.Errorf("leere Checkbox fehlt:\n%s", flat)
	}
	// Nur die nicht geschützten 100 MB des Volume-Typs zählen, und der
	// unwiderrufliche Typ ist als solcher markiert.
	if !strings.Contains(flat, "☐ Verwaiste Volumes ⚠ · 100.0 MB") {
		t.Errorf("Größe je Typ fehlt, zählt geschützte Objekte mit oder Warnhinweis fehlt:\n%s", flat)
	}

	a.Press(cbToggle + janitor.TypeImagesDangling)
	flat = flatten(a.Keyboard())
	if !strings.Contains(flat, "☑ Dangling Images") {
		t.Errorf("gesetzte Checkbox fehlt:\n%s", flat)
	}
	if !strings.Contains(flat, "✅ Auswahl bereinigen · 1 · 400.0 MB") {
		t.Errorf("Bestätigen-Button zeigt die Auswahl nicht an:\n%s", flat)
	}
}

// Bei nur einem Typ wären Toggles ein Klick mehr ohne Nutzen.
func TestApprovalSingleTypeStaysSimple(t *testing.T) {
	a := newApproval(testPlan(), []string{janitor.TypeVolumes})
	flat := flatten(a.Keyboard())
	if strings.Contains(flat, "☐") || strings.Contains(flat, "Auswahl bereinigen") {
		t.Errorf("einzelner Typ soll ohne Toggles auskommen:\n%s", flat)
	}
	if act := a.Press(cbAll); !act.Done || len(a.Chosen()) != 1 {
		t.Error("'Alles' muss auch bei einem einzelnen Typ greifen")
	}
}

func TestApprovalSummary(t *testing.T) {
	a := newTestApproval()
	if got := a.Summary(); !strings.Contains(got, "Nichts") {
		t.Errorf("leere Auswahl: Summary() = %q", got)
	}
	a.Press(cbToggle + janitor.TypeImagesDangling)
	a.Press(cbToggle + janitor.TypeBuildCache)
	got := a.Summary()
	for _, want := range []string{"Dangling Images", "Build-Cache", "450.0 MB"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary() = %q, enthält %q nicht", got, want)
		}
	}
}

// flatten renders a keyboard as text, one button per line.
func flatten(rows [][]notify.Button) string {
	var b strings.Builder
	for _, row := range rows {
		for _, btn := range row {
			b.WriteString(btn.Text)
			b.WriteString(" [" + btn.Data + "]\n")
		}
	}
	return b.String()
}
