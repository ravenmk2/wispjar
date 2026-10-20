// Package computer owns user computers and their durable lifecycle. Runtime
// consumers use this package without exposing SDK lifecycle operations.
package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/store"
	"github.com/ravenmk2/wispjar/internal/volume"
)

var (
	ErrNotFound    = errors.New("computer/not_found")
	ErrRequired    = errors.New("computer/required")
	ErrBusy        = errors.New("computer/busy")
	ErrInUse       = errors.New("computer/in_use")
	ErrUnavailable = errors.New("computer/unavailable")
	ErrConflict    = errors.New("operation/conflict")
	ErrInvalid     = errors.New("computer/invalid")
)

type Config struct {
	Image         string            `json:"image"`
	Resources     map[string]string `json:"resources"`
	Env           map[string]string `json:"env"`
	TTLSeconds    int               `json:"ttl_seconds"`
	NetworkPolicy json.RawMessage   `json:"network_policy,omitempty"`
}

func (c Config) Options() sandbox.Options {
	return sandbox.Options{Image: c.Image, ResourceLimits: c.Resources, Env: c.Env,
		TTLSeconds: c.TTLSeconds, NetworkPolicy: c.NetworkPolicy}
}

func mergeConfig(base, override Config) Config {
	if override.Image != "" {
		base.Image = override.Image
	}
	if override.Resources != nil {
		base.Resources = override.Resources
	}
	env := make(map[string]string, len(base.Env)+len(override.Env))
	for k, v := range base.Env {
		env[k] = v
	}
	for k, v := range override.Env {
		env[k] = v
	}
	base.Env = env
	if override.TTLSeconds != 0 {
		base.TTLSeconds = override.TTLSeconds
	}
	if len(override.NetworkPolicy) > 0 {
		base.NetworkPolicy = override.NetworkPolicy
	}
	return base
}

func validateConfig(c Config) error {
	if strings.TrimSpace(c.Image) == "" || c.TTLSeconds < 60 || c.TTLSeconds > 604800 {
		return fmt.Errorf("%w: image and TTL (60..604800) required", ErrInvalid)
	}
	if err := sandbox.ValidateEnv(c.Env); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for key, value := range c.Resources {
		if key != "cpu" && key != "memory" {
			return fmt.Errorf("%w: unsupported resource", ErrInvalid)
		}
		if key == "cpu" {
			n, err := strconv.ParseFloat(strings.TrimSuffix(value, "m"), 64)
			if err != nil || math.IsNaN(n) || n <= 0 || n > 1e6 {
				return fmt.Errorf("%w: invalid cpu", ErrInvalid)
			}
		} else if !memoryQuantity.MatchString(value) {
			return fmt.Errorf("%w: invalid memory", ErrInvalid)
		}
	}
	return nil
}

var memoryQuantity = regexp.MustCompile(`^[1-9][0-9]*(\.[0-9]+)?([EPTGMK]i?|[eptgmk])?$`)

type CreateInput struct {
	Name    string `json:"name"`
	AgentID string `json:"agent_id,omitempty"`
	Config  Config `json:"config"`
}
type PatchInput struct {
	Name *string           `json:"name,omitempty"`
	Env  *sandbox.EnvPatch `json:"env,omitempty"`
}
type RebuildInput struct {
	Config *Config           `json:"config,omitempty"`
	Env    *sandbox.EnvPatch `json:"env,omitempty"`
}
type RecoverInput struct {
	ConfirmStopAll bool `json:"confirm_stop_all"`
}

type Volumes interface {
	Ensure(context.Context, string, string) ([]volume.Mount, error)
	List(context.Context, string, string) ([]store.UserVolume, error)
}

// Backend must validate the entire identity before connecting or mutating.
// Ensure may adopt an existing generation after a lost create response.
type Backend interface {
	Ensure(context.Context, sandbox.ComputerSpec, bool) (*sandbox.Handle, error)
	Inspect(context.Context, sandbox.ComputerSpec) (string, error)
	Pause(context.Context, sandbox.ComputerSpec) error
	Delete(context.Context, sandbox.ComputerSpec) error
	Renew(context.Context, sandbox.ComputerSpec) error
}

// TerminalRun is deliberately conservative: lease expiration does not prove
// the associated remote commands have stopped.
func TerminalRun(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled"
}

type operationPlan struct {
	Generation         uint64            `json:"generation"`
	PreviousGeneration uint64            `json:"previous_generation,omitempty"`
	PreviousSandboxID  *string           `json:"previous_sandbox_id,omitempty"`
	CreateAttempted    bool              `json:"create_attempted,omitempty"`
	RunsRecovered      bool              `json:"runs_recovered,omitempty"`
	Cleanup            []retiredInstance `json:"cleanup,omitempty"`
}
type retiredInstance struct {
	Generation uint64  `json:"generation"`
	SandboxID  *string `json:"sandbox_id,omitempty"`
}
