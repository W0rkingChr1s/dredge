package janitor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/W0rkingChr1s/dredge/internal/config"
	"github.com/W0rkingChr1s/dredge/internal/docker"
)

type mockEngine struct {
	mu            sync.Mutex
	deletedImages []string
	deletedVols   []string
	deletedConts  []string
	deletedNets   []string
	buildPruned   bool
	now           time.Time
}

func (m *mockEngine) handler() http.Handler {
	old := m.now.Add(-100 * 24 * time.Hour).Unix()
	young := m.now.Add(-1 * time.Hour).Unix()
	oldRFC := m.now.Add(-100 * 24 * time.Hour).Format(time.RFC3339)

	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OK")) })

	mux.HandleFunc("/images/json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[
			{"Id":"sha256:danglingOld","RepoTags":["<none>:<none>"],"Created":%d,"Size":1048576,"Labels":{}},
			{"Id":"sha256:danglingYoung","RepoTags":["<none>:<none>"],"Created":%d,"Size":2097152,"Labels":{}},
			{"Id":"sha256:danglingKeep","RepoTags":["<none>:<none>"],"Created":%d,"Size":4194304,"Labels":{"janitor.keep":"true"}},
			{"Id":"sha256:used","RepoTags":["nginx:latest"],"Created":%d,"Size":8388608,"Labels":{}}
		]`, old, young, old, old)
	})

	mux.HandleFunc("/containers/json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[
			{"Id":"running1","Names":["/web"],"Image":"nginx:latest","ImageID":"sha256:used","Created":%d,"State":"running","Status":"Up 3 days","Labels":{},"SizeRw":0},
			{"Id":"exitedOld","Names":["/old-job"],"Image":"busybox","ImageID":"sha256:bb","Created":%d,"State":"exited","Status":"Exited (0) 5 days ago","Labels":{},"SizeRw":524288}
		]`, old, old)
	})

	mux.HandleFunc("/volumes", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"Volumes":[
			{"Name":"orphan1","Driver":"local","CreatedAt":%q,"Labels":{}},
			{"Name":"portainer_keep","Driver":"local","CreatedAt":%q,"Labels":{}},
			{"Name":"in_use_vol","Driver":"local","CreatedAt":%q,"Labels":{}}
		],"Warnings":[]}`, oldRFC, oldRFC, oldRFC)
	})

	mux.HandleFunc("/networks", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[
			{"Id":"netbridge","Name":"bridge","Created":%q,"Containers":{}},
			{"Id":"netunused","Name":"test_net","Created":%q,"Containers":{}},
			{"Id":"netused","Name":"prod_net","Created":%q,"Containers":{"c1":{}}}
		]`, oldRFC, oldRFC, oldRFC)
	})

	mux.HandleFunc("/system/df", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{
			"LayersSize": 123,
			"Volumes":[
				{"Name":"orphan1","UsageData":{"Size":3145728,"RefCount":0}},
				{"Name":"portainer_keep","UsageData":{"Size":1048576,"RefCount":0}},
				{"Name":"in_use_vol","UsageData":{"Size":9999999,"RefCount":1}}
			],
			"BuildCache":[
				{"ID":"bc1","Size":10485760,"InUse":false,"Shared":false,"LastUsedAt":%q},
				{"ID":"bc2","Size":5242880,"InUse":true,"Shared":false,"LastUsedAt":%q}
			]
		}`, oldRFC, oldRFC)
	})

	// deletes
	mux.HandleFunc("/images/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			m.mu.Lock()
			m.deletedImages = append(m.deletedImages, strings.TrimPrefix(r.URL.Path, "/images/"))
			m.mu.Unlock()
			w.Write([]byte(`[{"Deleted":"x"}]`))
		}
	})
	mux.HandleFunc("/containers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			m.mu.Lock()
			m.deletedConts = append(m.deletedConts, strings.TrimPrefix(r.URL.Path, "/containers/"))
			m.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("/volumes/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			m.mu.Lock()
			m.deletedVols = append(m.deletedVols, strings.TrimPrefix(r.URL.Path, "/volumes/"))
			m.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("/networks/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			m.mu.Lock()
			m.deletedNets = append(m.deletedNets, strings.TrimPrefix(r.URL.Path, "/networks/"))
			m.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("/build/prune", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.buildPruned = true
		m.mu.Unlock()
		w.Write([]byte(`{"CachesDeleted":["bc1"],"SpaceReclaimed":10485760}`))
	})
	return mux
}

func testConfig() *config.Config {
	c := config.Default()
	c.Resources.ImagesDangling = config.ResourceRule{Enabled: true, Mode: config.ModeAsk}
	c.Resources.ImagesUnused = config.ResourceRule{Enabled: false, Mode: config.ModeOff}
	c.Resources.Containers = config.ResourceRule{Enabled: true, Mode: config.ModeAuto}
	c.Resources.Networks = config.ResourceRule{Enabled: true, Mode: config.ModeAuto}
	c.Resources.BuildCache = config.ResourceRule{Enabled: true, Mode: config.ModeAuto}
	c.Resources.Volumes = config.ResourceRule{Enabled: true, Mode: config.ModeAsk}
	c.Safety.MinAgeHours = 24
	c.Safety.ProtectLabels = []string{"janitor.keep=true"}
	c.Safety.ProtectVolumeNames = []string{"portainer_*"}
	return c
}

func TestScanAndExecute(t *testing.T) {
	m := &mockEngine{now: time.Now()}
	srv := httptest.NewServer(m.handler())
	defer srv.Close()

	cli, err := docker.New(srv.URL, "", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	ctx := context.Background()

	plan, err := Scan(ctx, cli, cfg)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Dangling images: danglingOld removable; young + keep protected.
	dg := plan.Group(TypeImagesDangling)
	if got := dg.RemovableCount(); got != 1 {
		t.Errorf("dangling removable = %d, want 1", got)
	}
	if got := len(dg.Protected()); got != 2 {
		t.Errorf("dangling protected = %d, want 2", got)
	}

	// Containers: only exitedOld removable.
	cg := plan.Group(TypeContainers)
	if got := cg.RemovableCount(); got != 1 {
		t.Errorf("containers removable = %d, want 1", got)
	}
	if cg.Removable()[0].ID != "exitedOld" {
		t.Errorf("wrong container: %s", cg.Removable()[0].ID)
	}

	// Networks: only test_net.
	ng := plan.Group(TypeNetworks)
	if got := ng.RemovableCount(); got != 1 || ng.Removable()[0].ID != "netunused" {
		t.Errorf("networks removable = %d (%v), want 1 netunused", got, ng.Removable())
	}

	// Build cache: aggregate of non-inuse = 10MB.
	bg := plan.Group(TypeBuildCache)
	if got := bg.RemovableSize(); got != 10485760 {
		t.Errorf("build cache size = %d, want 10485760", got)
	}

	// Volumes: orphan1 removable, portainer_keep protected, in_use_vol excluded.
	vg := plan.Group(TypeVolumes)
	if got := vg.RemovableCount(); got != 1 || vg.Removable()[0].ID != "orphan1" {
		t.Errorf("volumes removable = %d (%v), want 1 orphan1", got, vg.Removable())
	}
	if vg.Removable()[0].Size != 3145728 {
		t.Errorf("orphan1 size = %d, want 3145728", vg.Removable()[0].Size)
	}

	// --- Execute everything ---
	decisions := DecisionsForAuto(cfg) // containers, networks, build
	for _, tp := range AskTypes(cfg, plan) {
		decisions[tp] = true // dangling images + volumes
	}
	res := Execute(ctx, cli, plan, cfg, decisions)

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.deletedImages) != 1 || !strings.Contains(m.deletedImages[0], "danglingOld") {
		t.Errorf("deleted images = %v, want [danglingOld]", m.deletedImages)
	}
	if len(m.deletedConts) != 1 || m.deletedConts[0] != "exitedOld" {
		t.Errorf("deleted containers = %v", m.deletedConts)
	}
	if len(m.deletedNets) != 1 || m.deletedNets[0] != "netunused" {
		t.Errorf("deleted networks = %v", m.deletedNets)
	}
	if len(m.deletedVols) != 1 || m.deletedVols[0] != "orphan1" {
		t.Errorf("deleted volumes = %v", m.deletedVols)
	}
	if !m.buildPruned {
		t.Error("build cache not pruned")
	}
	// Freed = image1MB + containerRw0.5MB + volume3MB + build10MB (+ network 0)
	wantFreed := int64(1048576 + 524288 + 3145728 + 10485760)
	if res.TotalFreed() != wantFreed {
		t.Errorf("freed = %d, want %d", res.TotalFreed(), wantFreed)
	}
}

func TestDryRunDeletesNothing(t *testing.T) {
	m := &mockEngine{now: time.Now()}
	srv := httptest.NewServer(m.handler())
	defer srv.Close()
	cli, _ := docker.New(srv.URL, "", 10*time.Second)
	cfg := testConfig()
	cfg.Safety.DryRun = true

	plan, err := Scan(context.Background(), cli, cfg)
	if err != nil {
		t.Fatal(err)
	}
	decisions := DecisionsForAuto(cfg)
	for _, tp := range AskTypes(cfg, plan) {
		decisions[tp] = true
	}
	res := Execute(context.Background(), cli, plan, cfg, decisions)

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.deletedImages)+len(m.deletedConts)+len(m.deletedVols)+len(m.deletedNets) != 0 || m.buildPruned {
		t.Error("dry-run must not delete or prune anything")
	}
	if !res.DryRun || res.TotalRemoved() == 0 {
		t.Errorf("dry-run should still report would-be removals, got removed=%d", res.TotalRemoved())
	}
}
