package janitor

import (
	"context"
	"fmt"
	"time"

	"github.com/W0rkingChr1s/dredge/internal/config"
	"github.com/W0rkingChr1s/dredge/internal/docker"
)

// TypeResult holds the outcome for one resource type.
type TypeResult struct {
	Type    string   `json:"type"`
	Label   string   `json:"label"`
	Removed int      `json:"removed"`
	Freed   int64    `json:"freed_bytes"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// Result is the outcome of an execution.
type Result struct {
	Types  []TypeResult `json:"types"`
	DryRun bool         `json:"dry_run"`
}

// TotalFreed sums freed bytes.
func (r *Result) TotalFreed() int64 {
	var s int64
	for _, t := range r.Types {
		s += t.Freed
	}
	return s
}

// TotalRemoved sums removed items.
func (r *Result) TotalRemoved() int {
	var n int
	for _, t := range r.Types {
		n += t.Removed
	}
	return n
}

// TotalFailed sums failures.
func (r *Result) TotalFailed() int {
	var n int
	for _, t := range r.Types {
		n += t.Failed
	}
	return n
}

// execOrder makes sure dependent resources are removed first (containers before
// their images, etc.).
var execOrder = []string{
	TypeContainers,
	TypeImagesUnused,
	TypeImagesDangling,
	TypeNetworks,
	TypeBuildCache,
	TypeVolumes,
}

// Execute removes the removable candidates of every approved type.
// approved maps a resource type to whether it should be executed.
func Execute(ctx context.Context, cli *docker.Client, plan *Plan, cfg *config.Config, approved map[string]bool) *Result {
	res := &Result{DryRun: cfg.Safety.DryRun}

	for _, typ := range execOrder {
		if !approved[typ] {
			continue
		}
		g := plan.Group(typ)
		if g == nil {
			continue
		}
		tr := TypeResult{Type: typ, Label: g.Label}

		if typ == TypeBuildCache {
			executeBuildCache(ctx, cli, g, cfg, &tr)
			res.Types = append(res.Types, tr)
			continue
		}

		for _, c := range g.Removable() {
			if cfg.Safety.DryRun {
				tr.Removed++
				tr.Freed += c.Size
				continue
			}
			if err := removeOne(ctx, cli, typ, c); err != nil {
				tr.Failed++
				if len(tr.Errors) < 10 {
					tr.Errors = append(tr.Errors, fmt.Sprintf("%s: %v", c.Name, err))
				}
				continue
			}
			tr.Removed++
			tr.Freed += c.Size
		}
		res.Types = append(res.Types, tr)
	}
	return res
}

func executeBuildCache(ctx context.Context, cli *docker.Client, g *Group, cfg *config.Config, tr *TypeResult) {
	rem := g.Removable()
	if len(rem) == 0 {
		return
	}
	if cfg.Safety.DryRun {
		tr.Removed += len(rem)
		tr.Freed += g.RemovableSize()
		return
	}
	until := ""
	if cfg.Safety.MinAgeHours > 0 {
		until = fmt.Sprintf("%dh", cfg.Safety.MinAgeHours)
	}
	pr, err := cli.PruneBuildCache(ctx, until)
	if err != nil {
		tr.Failed++
		tr.Errors = append(tr.Errors, err.Error())
		return
	}
	tr.Removed += len(pr.CachesDeleted)
	tr.Freed += pr.SpaceReclaimed
}

func removeOne(ctx context.Context, cli *docker.Client, typ string, c Candidate) error {
	switch typ {
	case TypeImagesDangling, TypeImagesUnused:
		return cli.RemoveImage(ctx, c.ID, false)
	case TypeContainers:
		return cli.RemoveContainer(ctx, c.ID, false)
	case TypeNetworks:
		return cli.RemoveNetwork(ctx, c.ID)
	case TypeVolumes:
		return cli.RemoveVolume(ctx, c.ID, false)
	default:
		return fmt.Errorf("unbekannter Typ %s", typ)
	}
}

// DecisionsForAuto builds the initial approval map from the config: every
// enabled type in ModeAuto is pre-approved. Ask/off are left false.
func DecisionsForAuto(cfg *config.Config) map[string]bool {
	m := map[string]bool{}
	for _, r := range cfg.Resources.All() {
		if r.Rule.Enabled && r.Rule.Mode == config.ModeAuto {
			m[r.Key] = true
		}
	}
	return m
}

// AskTypes returns the enabled types in ModeAsk that actually have something to
// remove (so we don't ask about empty groups).
func AskTypes(cfg *config.Config, plan *Plan) []string {
	var out []string
	for _, r := range cfg.Resources.All() {
		if !r.Rule.Enabled || r.Rule.Mode != config.ModeAsk {
			continue
		}
		if g := plan.Group(r.Key); g != nil && g.RemovableCount() > 0 {
			out = append(out, r.Key)
		}
	}
	return out
}

// FormatDuration is a tiny helper for logging elapsed time.
func FormatDuration(d time.Duration) string {
	return d.Round(time.Second).String()
}
