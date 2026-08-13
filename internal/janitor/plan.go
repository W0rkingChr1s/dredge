// Package janitor contains the scan/plan/execute logic that decides what to
// remove and carries it out, honouring all safety rails.
package janitor

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/W0rkingChr1s/dredge/internal/config"
	"github.com/W0rkingChr1s/dredge/internal/docker"
)

// Resource type keys (must match config.Resources.All keys).
const (
	TypeImagesDangling = "images_dangling"
	TypeImagesUnused   = "images_unused"
	TypeContainers     = "containers"
	TypeNetworks       = "networks"
	TypeBuildCache     = "build_cache"
	TypeVolumes        = "volumes"
)

// Candidate is a single removable (or protected) resource.
type Candidate struct {
	Type      string
	ID        string  // engine ID / volume name / network ID
	Name      string  // display name
	Size      int64   // bytes (may be 0/unknown)
	AgeHours  float64 // age in hours (may be 0 if unknown)
	Protected bool    // true => kept
	Reason    string  // why protected
}

// Group bundles candidates of one resource type together with its rule.
type Group struct {
	Type       string
	Label      string
	Rule       config.ResourceRule
	Candidates []Candidate
}

// Removable returns the non-protected candidates.
func (g Group) Removable() []Candidate {
	out := make([]Candidate, 0, len(g.Candidates))
	for _, c := range g.Candidates {
		if !c.Protected {
			out = append(out, c)
		}
	}
	return out
}

// Protected returns the protected candidates.
func (g Group) Protected() []Candidate {
	var out []Candidate
	for _, c := range g.Candidates {
		if c.Protected {
			out = append(out, c)
		}
	}
	return out
}

// RemovableSize sums the sizes of removable candidates.
func (g Group) RemovableSize() int64 {
	var s int64
	for _, c := range g.Removable() {
		s += c.Size
	}
	return s
}

// RemovableCount counts removable candidates.
func (g Group) RemovableCount() int { return len(g.Removable()) }

// Plan is the full result of a scan.
type Plan struct {
	Groups    []Group
	ScannedAt time.Time
}

// Group returns the group for a type, or nil.
func (p *Plan) Group(t string) *Group {
	for i := range p.Groups {
		if p.Groups[i].Type == t {
			return &p.Groups[i]
		}
	}
	return nil
}

// TotalRemovableSize sums removable sizes across all groups.
func (p *Plan) TotalRemovableSize() int64 {
	var s int64
	for _, g := range p.Groups {
		s += g.RemovableSize()
	}
	return s
}

// TotalRemovableCount sums removable counts across all groups.
func (p *Plan) TotalRemovableCount() int {
	var n int
	for _, g := range p.Groups {
		n += g.RemovableCount()
	}
	return n
}

// HasAnything reports whether there is at least one removable candidate.
func (p *Plan) HasAnything() bool { return p.TotalRemovableCount() > 0 }

// Scan inspects the engine and builds a Plan, applying all safety rails.
func Scan(ctx context.Context, cli *docker.Client, cfg *config.Config) (*Plan, error) {
	now := time.Now()
	plan := &Plan{ScannedAt: now}

	df, err := cli.DiskUsage(ctx)
	if err != nil {
		return nil, err
	}
	// Volume-name -> size from df.
	volSize := map[string]int64{}
	volRef := map[string]int64{}
	for _, v := range df.Volumes {
		if v.UsageData != nil {
			volSize[v.Name] = v.UsageData.Size
			volRef[v.Name] = v.UsageData.RefCount
		}
	}

	rules := cfg.Resources
	safety := cfg.Safety

	// ---- Images ----
	images, err := cli.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	containers, err := cli.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	inUseImage := map[string]bool{}
	for _, c := range containers {
		if c.ImageID != "" {
			inUseImage[c.ImageID] = true
		}
		inUseImage[c.Image] = true
	}

	if rules.ImagesDangling.Enabled {
		g := Group{Type: TypeImagesDangling, Label: "Dangling Images", Rule: rules.ImagesDangling}
		for _, img := range images {
			if !img.Dangling() {
				continue
			}
			c := Candidate{Type: g.Type, ID: img.ID, Name: shortID(img.ID) + " " + img.Ref(),
				Size: img.Size, AgeHours: hoursSince(now, time.Unix(img.Created, 0))}
			applyImageSafety(&c, img, safety, now)
			g.Candidates = append(g.Candidates, c)
		}
		plan.Groups = append(plan.Groups, g)
	}

	if rules.ImagesUnused.Enabled {
		g := Group{Type: TypeImagesUnused, Label: "Ungenutzte Images", Rule: rules.ImagesUnused}
		for _, img := range images {
			if img.Dangling() {
				continue // handled by dangling group
			}
			if inUseImage[img.ID] {
				continue
			}
			c := Candidate{Type: g.Type, ID: img.ID, Name: img.Ref(),
				Size: img.Size, AgeHours: hoursSince(now, time.Unix(img.Created, 0))}
			applyImageSafety(&c, img, safety, now)
			g.Candidates = append(g.Candidates, c)
		}
		plan.Groups = append(plan.Groups, g)
	}

	// ---- Containers (stopped) ----
	if rules.Containers.Enabled {
		g := Group{Type: TypeContainers, Label: "Gestoppte Container", Rule: rules.Containers}
		for _, ct := range containers {
			if isRunningState(ct.State) {
				continue
			}
			c := Candidate{Type: g.Type, ID: ct.ID, Name: ct.Name() + " (" + ct.Status + ")",
				Size: ct.SizeRw, AgeHours: hoursSince(now, time.Unix(ct.Created, 0))}
			applyLabelAge(&c, ct.Labels, safety, now)
			g.Candidates = append(g.Candidates, c)
		}
		plan.Groups = append(plan.Groups, g)
	}

	// ---- Networks (unused, user-defined) ----
	if rules.Networks.Enabled {
		g := Group{Type: TypeNetworks, Label: "Ungenutzte Netzwerke", Rule: rules.Networks}
		networks, err := cli.ListNetworks(ctx)
		if err != nil {
			return nil, err
		}
		for _, n := range networks {
			if isPredefinedNetwork(n.Name) {
				continue
			}
			if len(n.Containers) > 0 {
				continue // in use
			}
			c := Candidate{Type: g.Type, ID: n.ID, Name: n.Name,
				AgeHours: hoursSinceRFC(now, n.Created)}
			applyLabelAge(&c, n.Labels, safety, now)
			g.Candidates = append(g.Candidates, c)
		}
		plan.Groups = append(plan.Groups, g)
	}

	// ---- Build cache (aggregate) ----
	if rules.BuildCache.Enabled {
		g := Group{Type: TypeBuildCache, Label: "Build-Cache", Rule: rules.BuildCache}
		var size int64
		var count int
		var newest float64 = 1e18
		for _, b := range df.BuildCache {
			if b.InUse || b.Shared {
				continue
			}
			size += b.Size
			count++
			age := hoursSinceRFC(now, b.LastUsedAt)
			if age < newest {
				newest = age
			}
		}
		if count > 0 {
			c := Candidate{Type: g.Type, ID: "build-cache", Name: pluralRecords(count),
				Size: size, AgeHours: newest}
			// Build cache respects only the age rail (enforced via prune "until").
			if safety.MinAgeHours > 0 && newest < float64(safety.MinAgeHours) {
				// Some records are younger than the threshold; the prune call
				// itself will keep them via the "until" filter, so we still
				// list the group as removable but note it.
				c.Reason = "teilweise jünger als Mindestalter (wird per until gefiltert)"
			}
			g.Candidates = append(g.Candidates, c)
		}
		plan.Groups = append(plan.Groups, g)
	}

	// ---- Volumes (orphaned) ----
	if rules.Volumes.Enabled {
		g := Group{Type: TypeVolumes, Label: "Verwaiste Volumes", Rule: rules.Volumes}
		vols, err := cli.ListVolumes(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range vols {
			// Orphaned = referenced by no container. df RefCount is authoritative
			// when present; if absent, fall back to "assume unused" cautiously.
			ref, known := volRef[v.Name]
			if known && ref > 0 {
				continue
			}
			c := Candidate{Type: g.Type, ID: v.Name, Name: v.Name,
				Size: volSize[v.Name], AgeHours: hoursSinceRFC(now, v.CreatedAt)}
			applyVolumeSafety(&c, v, safety, now)
			g.Candidates = append(g.Candidates, c)
		}
		plan.Groups = append(plan.Groups, g)
	}

	return plan, nil
}

// ---- safety helpers ----

func applyImageSafety(c *Candidate, img docker.Image, s config.Safety, now time.Time) {
	if labelProtected(img.Labels, s.ProtectLabels) {
		c.Protected, c.Reason = true, "Schutz-Label"
		return
	}
	for _, pat := range s.ProtectImageRepos {
		for _, t := range img.RepoTags {
			if globMatch(pat, t) {
				c.Protected, c.Reason = true, "Repo geschützt ("+pat+")"
				return
			}
		}
	}
	ageProtect(c, s, now)
}

func applyVolumeSafety(c *Candidate, v docker.Volume, s config.Safety, now time.Time) {
	if labelProtected(v.Labels, s.ProtectLabels) {
		c.Protected, c.Reason = true, "Schutz-Label"
		return
	}
	for _, pat := range s.ProtectVolumeNames {
		if globMatch(pat, v.Name) {
			c.Protected, c.Reason = true, "Name geschützt ("+pat+")"
			return
		}
	}
	ageProtect(c, s, now)
}

func applyLabelAge(c *Candidate, labels map[string]string, s config.Safety, now time.Time) {
	if labelProtected(labels, s.ProtectLabels) {
		c.Protected, c.Reason = true, "Schutz-Label"
		return
	}
	ageProtect(c, s, now)
}

func ageProtect(c *Candidate, s config.Safety, now time.Time) {
	if s.MinAgeHours > 0 && c.AgeHours >= 0 && c.AgeHours < float64(s.MinAgeHours) {
		c.Protected = true
		c.Reason = "jünger als Mindestalter"
	}
}

func labelProtected(labels map[string]string, protect []string) bool {
	if len(labels) == 0 {
		return false
	}
	for _, p := range protect {
		if k, v, ok := strings.Cut(p, "="); ok {
			if labels[k] == v {
				return true
			}
		} else if _, ok := labels[p]; ok {
			return true
		}
	}
	return false
}

func globMatch(pattern, s string) bool {
	ok, err := path.Match(pattern, s)
	return err == nil && ok
}

func hoursSince(now, t time.Time) float64 {
	if t.IsZero() || t.Unix() <= 0 {
		return -1
	}
	return now.Sub(t).Hours()
}

func hoursSinceRFC(now time.Time, s string) float64 {
	if s == "" {
		return -1
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return now.Sub(t).Hours()
		}
	}
	return -1
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func isRunningState(state string) bool {
	switch strings.ToLower(state) {
	case "running", "paused", "restarting", "removing":
		return true
	}
	return false
}

func isPredefinedNetwork(name string) bool {
	switch name {
	case "bridge", "host", "none":
		return true
	}
	return false
}

func pluralRecords(n int) string {
	if n == 1 {
		return "1 Eintrag"
	}
	return itoa(n) + " Einträge"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
