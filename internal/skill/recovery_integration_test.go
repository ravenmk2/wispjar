package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

type installFunc func(context.Context, InstallPlan) error

func (f installFunc) Install(ctx context.Context, p InstallPlan) error { return f(ctx, p) }

func TestRecoverComputerProvenanceIntegration(t *testing.T) {
	f := integrationFixture(t)
	ctx := context.Background()
	sk, err := f.svc.Upload(f.user, f.org, uploadHeader(t, "recover-skill"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.db.Create(&store.AgentSkill{AgentID: f.agents[0], SkillID: sk.ID}).Error; err != nil {
		t.Fatal(err)
	}
	r := f.run(t, f.agents[0])
	var installed InstallPlan
	_, err = f.c.Acquire(ctx, r, installFunc(func(_ context.Context, p InstallPlan) error {
		installed = p
		return fmt.Errorf("connection lost")
	}))
	requireCode(t, err, "skill/sync_failed")
	var root store.ComputerRun
	if err = f.db.First(&root, "id = ?", r.RootRunID).Error; err != nil {
		t.Fatal(err)
	}
	if installed.RootRunID != root.ID || installed.ComputerID != root.ComputerID || installed.Generation != 7 {
		t.Fatalf("installer provenance not obtained from durable run: %+v", installed)
	}
	requireCode(t, f.c.RecoverComputer(ctx, f.org, f.user, root.ComputerID, 7), "skill/sync_blocked")

	// Synthetic durable operations cover old unassociated plans, another
	// computer, another generation, and stale fence: none is recoverable here.
	untouched := map[string]uint64{}
	for index, variant := range []string{"legacy", "other-computer", "other-generation", "stale-fence"} {
		extra, err := f.svc.Upload(f.user, f.org, uploadHeader(t, "recover-"+variant))
		if err != nil {
			t.Fatal(err)
		}
		p := installed
		p.SkillID, p.Name, p.Owner = extra.ID, "recover-"+variant, id.New()
		stateFence := p.Fence
		switch index {
		case 0:
			p.RootRunID, p.ComputerID, p.Generation = "", "", 0
		case 1:
			p.ComputerID = id.New()
		case 2:
			p.Generation = 8
		case 3:
			stateFence++
		}
		raw, _ := json.Marshal(p)
		if err := f.db.Create(&store.ResourceOperation{ID: id.New(), OrgID: f.org, UserID: f.user, ResourceType: "skill", ResourceID: extra.ID, Action: "skill/sync", Status: "blocked", Progress: raw}).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Create(&store.UserSkillState{OrgID: f.org, UserID: f.user, SkillID: extra.ID, Status: "blocked", DesiredRevision: 1, Fence: stateFence}).Error; err != nil {
			t.Fatal(err)
		}
		untouched[extra.ID] = stateFence
	}
	if err = f.db.Model(&store.ComputerRun{}).Where("id = ?", root.ID).Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	if err = f.db.Create(&store.RunSkillLease{RunID: root.ID, OrgID: f.org, UserID: f.user, SkillID: sk.ID, Revision: 1}).Error; err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err = f.c.RecoverComputer(ctx, f.org, f.user, root.ComputerID, 7); err != nil {
			t.Fatal(err)
		}
	}
	var state store.UserSkillState
	if err = f.db.Where("org_id = ? AND user_id = ? AND skill_id = ?", f.org, f.user, sk.ID).Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.Status != "pending" || state.Fence != installed.Fence+1 || state.AppliedRevision != 0 {
		t.Fatalf("recovery did not leave a fenced retry: %+v", state)
	}
	for skillID, fence := range untouched {
		var state store.UserSkillState
		if err := f.db.Where("org_id = ? AND user_id = ? AND skill_id = ?", f.org, f.user, skillID).Take(&state).Error; err != nil {
			t.Fatal(err)
		}
		if state.Status != "blocked" || state.Fence != fence {
			t.Fatalf("unrelated/unknown operation was recovered: %+v", state)
		}
	}
	var leases int64
	if err := f.db.Model(&store.RunSkillLease{}).Where("run_id = ?", root.ID).Count(&leases).Error; err != nil || leases != 0 {
		t.Fatalf("recovered run retained leases: %d %v", leases, err)
	}
	_, err = f.c.Acquire(ctx, r, installFunc(func(context.Context, InstallPlan) error {
		t.Error("finished run invoked installer")
		return nil
	}))
	requireCode(t, err, "skill/run_finished")
}

func TestRecoverComputerInFlightAcquireIntegration(t *testing.T) {
	f := integrationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sk, err := f.svc.Upload(f.user, f.org, uploadHeader(t, "inflight-skill"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.db.Create(&store.AgentSkill{AgentID: f.agents[0], SkillID: sk.ID}).Error; err != nil {
		t.Fatal(err)
	}
	r := f.run(t, f.agents[0])
	started := make(chan InstallPlan, 1)
	complete := make(chan struct{})
	acquired := make(chan error, 1)
	go func() {
		_, err := f.c.Acquire(ctx, r, installFunc(func(ctx context.Context, p InstallPlan) error {
			started <- p
			select {
			case <-complete:
				return nil // Late success after instance termination.
			case <-ctx.Done():
				return ctx.Err()
			}
		}))
		acquired <- err
	}()
	var plan InstallPlan
	select {
	case plan = <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Simulate the computer worker's durable stop phase while the skill
	// advisory lock is still held by remote IO in Acquire.
	if err = f.db.Model(&store.ComputerRun{}).Where("id = ?", r.RootRunID).Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	recovered := make(chan error, 1)
	go func() { recovered <- f.c.RecoverComputer(ctx, f.org, f.user, plan.ComputerID, plan.Generation) }()
	close(complete)
	requireCode(t, <-acquired, "skill/run_finished")
	if err = <-recovered; err != nil {
		t.Fatal(err)
	}
	var state store.UserSkillState
	if err = f.db.Where("org_id = ? AND user_id = ? AND skill_id = ?", f.org, f.user, sk.ID).Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.Status != "pending" || state.Fence <= plan.Fence || state.AppliedRevision != 0 {
		t.Fatalf("late installer published state after stop: %+v", state)
	}
	var leases int64
	if err = f.db.Model(&store.RunSkillLease{}).Where("run_id = ?", r.RootRunID).Count(&leases).Error; err != nil || leases != 0 {
		t.Fatalf("late Acquire resurrected leases: %d %v", leases, err)
	}
}
