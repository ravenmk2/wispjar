package computer

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/ravenmk2/wispjar/internal/sandbox"
)

func (s *Service) Start(ctx context.Context) {
	if s.cancel != nil {
		return
	}
	ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			s.recover(ctx)
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			case <-ticker.C:
				s.maintain(ctx)
			}
		}
	}()
}
func (s *Service) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}
func (s *Service) recover(ctx context.Context) {
	ops, err := s.repo.Pending(ctx)
	if err != nil {
		log.Printf("computer: pending operations: %v", err)
		return
	}
	for _, op := range ops {
		if ctx.Err() != nil {
			return
		}
		if err = s.Process(ctx, op.ID); err != nil {
			log.Printf("computer: operation %s: %v", op.ID, err)
		}
	}
}
func (s *Service) Process(ctx context.Context, operationID string) (err error) {
	if err := s.available(); err != nil {
		return err
	}
	op, err := s.repo.Operation(ctx, operationID)
	if err != nil {
		return err
	}
	unlock, err := s.repo.Lock(ctx, op.ResourceID)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	op, err = s.repo.Operation(ctx, operationID)
	if err != nil {
		return err
	}
	if op.Status != "pending" && op.Status != "running" {
		return nil
	}
	c, err := s.repo.Get(ctx, op.ResourceID)
	if err != nil {
		return err
	}
	var plan operationPlan
	if err = json.Unmarshal(op.Progress, &plan); err != nil {
		return err
	}
	if c.Generation != plan.Generation {
		return ErrConflict
	}
	spec, err := s.spec(c)
	if err != nil {
		return err
	}
	finish := func(sid *string, status string, cause error) error {
		if cause != nil {
			log.Printf("computer: operation=%s computer=%s action=%s: %v", op.ID, c.ID, op.Action, cause)
		}
		// Interrupted workers leave their intent recoverable, including an
		// ambiguous create. Never erase the creation-attempt marker.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.repo.Complete(ctx, op, sid, status, cause); err != nil {
			return err
		}
		s.changed(ctx, op.ResourceID)
		return nil
	}
	if op.Action == "computer/recover" {
		if err = s.repo.Progress(ctx, op.ID, plan); err != nil {
			return err
		}
		// A successful DELETE alone can be an asynchronous acknowledgement.
		// Only authoritative absence proves that no old command can mutate
		// shared files; transport/ownership failures retain every lease.
		instances := append([]retiredInstance{{Generation: plan.Generation, SandboxID: c.SandboxID}}, plan.Cleanup...)
		for _, retired := range instances {
			old := spec
			old.Generation = retired.Generation
			old.SandboxID = retired.SandboxID
			if err = s.backend.Delete(ctx, old); err != nil {
				return finish(nil, "recovering", err)
			}
			if _, err = s.backend.Inspect(ctx, old); !errors.Is(err, sandbox.ErrComputerMissing) {
				if err == nil {
					err = ErrBusy
				}
				return finish(nil, "recovering", err)
			}
		}
		if err = s.repo.RecoverRuns(ctx, op); err != nil {
			return finish(nil, "recovering", err)
		}
		if s.onRecovery == nil {
			return finish(nil, "recovering", ErrUnavailable)
		}
		if err = s.onRecovery(ctx, c); err != nil {
			return finish(nil, "recovering", err)
		}
		return finish(nil, "stopped", nil)
	}
	for _, retired := range plan.Cleanup {
		old := spec
		old.Generation = retired.Generation
		old.SandboxID = retired.SandboxID
		if err = s.backend.Delete(ctx, old); err != nil {
			return finish(nil, "failed", err)
		}
	}
	switch op.Action {
	case "computer/create", "computer/start", "computer/rebuild":
		if plan.PreviousGeneration != 0 {
			old := spec
			old.Generation = plan.PreviousGeneration
			old.SandboxID = plan.PreviousSandboxID
			if err = s.backend.Delete(ctx, old); err != nil {
				return finish(nil, "failed", err)
			}
		}
		spec.Mounts, err = s.volumes.Ensure(ctx, c.OrgID, c.UserID)
		if err != nil {
			return finish(nil, "failed", err)
		}
		allowCreate := !plan.CreateAttempted
		plan.CreateAttempted = true
		if err = s.repo.Progress(ctx, op.ID, plan); err != nil {
			return err
		}
		op.Progress, _ = json.Marshal(plan)
		h, err := s.backend.Ensure(ctx, spec, allowCreate)
		if err != nil {
			return finish(nil, "failed", err)
		}
		return finish(&h.SandboxID, "running", nil)
	case "computer/pause":
		err = s.backend.Pause(ctx, spec)
		return finish(c.SandboxID, "paused", err)
	case "computer/delete":
		err = s.backend.Delete(ctx, spec)
		return finish(nil, "deleted", err)
	}
	return ErrInvalid
}

func (s *Service) maintain(ctx context.Context) {
	if s.available() != nil {
		return
	}
	cs, err := s.repo.List(ctx, "", "")
	if err != nil {
		return
	}
	idle := 8 * time.Hour
	if s.settings != nil {
		if st, e := s.settings.Load(ctx); e == nil && st.IdleTTLSeconds > 0 {
			idle = time.Duration(st.IdleTTLSeconds) * time.Second
		}
	}
	for _, c := range cs {
		if ctx.Err() != nil {
			return
		}
		if c.Status != "running" && c.Status != "paused" {
			continue
		}
		unlock, e := s.repo.Lock(ctx, c.ID)
		if e != nil {
			continue
		}
		current, e := s.repo.Get(ctx, c.ID)
		if e == nil && current.Generation == c.Generation && current.Status == c.Status {
			spec, err := s.spec(current)
			if err == nil {
				err = s.backend.Renew(ctx, spec)
			}
			if err != nil {
				log.Printf("computer: renew %s: %v", c.ID, err)
			}
		}
		_ = unlock()
		if c.Status == "running" && c.LastActiveAt != nil && time.Since(*c.LastActiveAt) > idle {
			// Intent's locked run check closes the idle-check/task-start race.
			op, _ := newOperation(c.OrgID, c.UserID, c.ID, "pause", "", nil)
			op.Progress, _ = json.Marshal(map[string]any{"idle_before": time.Now().Add(-idle)})
			if _, e = s.repo.Intent(ctx, nil, op); e != nil && !errors.Is(e, ErrBusy) {
				log.Printf("computer: idle %s: %v", c.ID, e)
			}
			if e == nil {
				s.changed(ctx, c.ID)
			}
		}
	}
}
