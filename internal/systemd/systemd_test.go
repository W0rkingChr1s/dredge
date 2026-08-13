package systemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOptions(dir string) Options {
	return Options{
		Name:           DefaultName,
		UnitDir:        dir,
		ExecPath:       "/usr/local/bin/dredge",
		ConfigPath:     "/etc/dredge/config.yaml",
		StateDir:       "/var/lib/dredge",
		Cron:           "0 9 * * 1",
		Timezone:       "Europe/Berlin",
		Timeout:        135 * time.Minute,
		Persistent:     true,
		SystemdVersion: 254,
	}
}

func TestRenderService(t *testing.T) {
	service, _, _, err := testOptions(t.TempDir()).Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"Type=oneshot",
		`ExecStart="/usr/local/bin/dredge" run`,
		`Environment="DREDGE_CONFIG=/etc/dredge/config.yaml"`,
		// Ohne dieses Limit killt systemd den Lauf nach 90 s – mitten in
		// einer laufenden Telegram-Freigabe.
		"TimeoutStartSec=135min",
		"ProtectSystem=strict",
		`ReadWritePaths="/var/lib/dredge"`,
		"NoNewPrivileges=true",
	} {
		if !strings.Contains(service, want) {
			t.Errorf("Service-Unit enthält %q nicht:\n%s", want, service)
		}
	}
}

func TestRenderTimerTimezone(t *testing.T) {
	opt := testOptions(t.TempDir())
	_, timer, warns, err := opt.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(timer, "OnCalendar=Mon *-*-* 09:00:00 Europe/Berlin") {
		t.Errorf("Zeitzone fehlt im Timer:\n%s", timer)
	}
	if !strings.Contains(timer, "Persistent=true") {
		t.Errorf("Persistent fehlt:\n%s", timer)
	}
	if len(warns) != 0 {
		t.Errorf("unerwartete Warnungen: %v", warns)
	}

	// Ältere systemd-Versionen kennen den Zeitzonen-Suffix nicht.
	opt.SystemdVersion = 247
	_, timer, warns, err = opt.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(timer, "Europe/Berlin") {
		t.Errorf("systemd 247 darf keinen Zeitzonen-Suffix bekommen:\n%s", timer)
	}
	if len(warns) == 0 {
		t.Error("fehlende Warnung, dass die Zeitzone ignoriert wird")
	}
}

func TestRenderUser(t *testing.T) {
	opt := testOptions(t.TempDir())
	opt.User, opt.Group, opt.DockerGroup = "dredge", "dredge", true
	service, _, _, err := opt.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"User=dredge", "Group=dredge", "SupplementaryGroups=docker"} {
		if !strings.Contains(service, want) {
			t.Errorf("Service-Unit enthält %q nicht:\n%s", want, service)
		}
	}
}

func TestRenderRejectsBrokenCron(t *testing.T) {
	opt := testOptions(t.TempDir())
	opt.Cron = "jeden montag bitte"
	if _, _, _, err := opt.Render(); err == nil {
		t.Fatal("kaputter Cron-Ausdruck muss einen Fehler geben, nicht stillschweigend 09:00 werden")
	}
}

func TestWriteIsIdempotentAndRemovable(t *testing.T) {
	dir := t.TempDir()
	opt := testOptions(dir)

	changed, _, err := opt.Write()
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !changed {
		t.Error("erster Write muss changed melden")
	}
	for _, n := range []string{opt.ServiceName(), opt.TimerName()} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Errorf("%s wurde nicht geschrieben: %v", n, err)
		}
	}

	changed, _, err = opt.Write()
	if err != nil {
		t.Fatalf("zweiter Write: %v", err)
	}
	if changed {
		t.Error("unveränderter Write darf kein daemon-reload auslösen")
	}

	opt.Cron = "0 3 * * *"
	if changed, _, err = opt.Write(); err != nil || !changed {
		t.Errorf("geänderter Schedule muss changed melden (changed=%v, err=%v)", changed, err)
	}

	removed, err := opt.Remove()
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(removed) != 2 {
		t.Errorf("Remove entfernte %d Dateien, want 2", len(removed))
	}
	if removed, err := opt.Remove(); err != nil || len(removed) != 0 {
		t.Errorf("zweites Remove: removed=%v, err=%v", removed, err)
	}
}

func TestDurationSpec(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Minute:  "30min",
		2 * time.Hour:     "2h",
		135 * time.Minute: "135min",
		90 * time.Second:  "90s",
	}
	for in, want := range cases {
		if got := durationSpec(in); got != want {
			t.Errorf("durationSpec(%v) = %q, want %q", in, got, want)
		}
	}
}
