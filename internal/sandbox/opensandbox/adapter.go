package opensandbox

import (
	"errors"
	"fmt"
	"strings"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/ravenmk2/wispjar/internal/sandbox"
)

// Local aliases keep adapter internals concise; all public contracts belong to
// sandbox and no SDK DTO escapes this package.
type ComputerSpec = sandbox.ComputerSpec
type Options = sandbox.Options
type CommandResult = sandbox.CommandResult
type FileInfo = sandbox.FileInfo
type FileReader = sandbox.FileReader
type FileEntry = sandbox.FileEntry

var (
	ErrComputerIdentity   = sandbox.ErrComputerIdentity
	ErrComputerMissing    = sandbox.ErrComputerMissing
	ErrComputerAmbiguous  = sandbox.ErrComputerAmbiguous
	ErrComputerInsecure   = sandbox.ErrComputerInsecure
	ErrFileAPIUnavailable = sandbox.ErrFileAPIUnavailable
)

var _ sandbox.Backend = (*ComputerBackend)(nil)
var _ sandbox.MaintenanceProvider = (*ComputerBackend)(nil)
var _ sandbox.Pausable = (*ComputerBackend)(nil)
var _ sandbox.Renewable = (*ComputerBackend)(nil)
var _ sandbox.Maintainer = (*Handle)(nil)
var _ sandbox.Instance = (*instance)(nil)

// A separate wrapper prevents a workload consumer from asserting Instance to
// the privileged Handle, even though both share the same authenticated client.
type instance struct{ handle *Handle }

func (i *instance) Ref() sandbox.InstanceRef           { return i.handle.ref }
func (i *instance) Files() (sandbox.FileReader, error) { return i.handle.UserFiles() }

func (b *ComputerBackend) Maintainer(value sandbox.Instance) (sandbox.Maintainer, error) {
	i, ok := value.(*instance)
	if !ok || i == nil || i.handle == nil || i.handle.owner != b {
		return nil, sandbox.ErrComputerIdentity
	}
	return i.handle, nil
}

// Preserve the original cause for internal diagnostics and errors.As while
// exposing stable categories to consumers that do not import the SDK.
func backendError(err error) error {
	if err == nil {
		return nil
	}
	var api *sdk.APIError
	if !errors.As(err, &api) {
		return err
	}
	var category error
	switch {
	case api.StatusCode == 401 || api.StatusCode == 403:
		category = sandbox.ErrBackendAuth
	case strings.Contains(strings.ToUpper(api.Response.Code), "IMAGE"):
		category = sandbox.ErrImageUnavailable
	case api.StatusCode == 409:
		category = sandbox.ErrConflict
	case api.StatusCode >= 500:
		category = sandbox.ErrUnavailable
	default:
		return err
	}
	return fmt.Errorf("%w: %w", category, err)
}
