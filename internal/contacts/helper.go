package contacts

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"

	"github.com/jimmingcheng/safe-imsg/internal/config"
	"github.com/jimmingcheng/safe-imsg/internal/rpc"
	"github.com/jimmingcheng/safe-imsg/internal/securefile"
)

const maxOutput = 4 << 20

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Container struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Type   string  `json:"type"`
	Groups []Group `json:"groups"`
}
type Discovery struct {
	Containers []Container `json:"containers"`
}

// CheckHelper is also used on every execution so later file replacement cannot
// silently weaken the trusted path. Discovery remains an owner-local command.
func CheckHelper(path string) error {
	if runtime.GOOS != "darwin" {
		return Unsupported
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return UnsafeHelper
	}
	if err := securefile.CheckOwnerFile(path, true); err != nil {
		return UnsafeHelper
	}
	_, err := helperBundle(path)
	return err
}

func helperBundle(path string) (string, error) {
	bundle := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	if filepath.Ext(bundle) != ".app" || filepath.Join(bundle, "Contents", "MacOS", "safe-imsg-contacts") != path {
		return "", UnsafeHelper
	}
	if err := securefile.CheckOwnerFile(filepath.Join(bundle, "Contents", "Info.plist"), false); err != nil {
		return "", UnsafeHelper
	}
	return bundle, nil
}

func runHelper(ctx context.Context, path string, request any, result any) error {
	if err := CheckHelper(path); err != nil {
		return err
	}
	return executeDesktopHelper(ctx, path, request, result)
}

func decodeHelperResponse(output []byte, result any) error {
	var envelope struct {
		V      int             `json:"v"`
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result,omitempty"`
		Code   string          `json:"code,omitempty"`
	}
	if rpc.DecodeParams(output, &envelope) != nil || envelope.V != 1 {
		return Invalid
	}
	if !envelope.OK {
		if len(envelope.Result) != 0 {
			return Invalid
		}
		code := Error(envelope.Code)
		if ErrorCode(code) != code {
			return Invalid
		}
		return code
	}
	if envelope.Code != "" || rpc.DecodeParams(envelope.Result, result) != nil {
		return Invalid
	}
	return nil
}

func Fetch(ctx context.Context, cfg config.ContactsSource) (Snapshot, error) {
	var result Snapshot
	err := runHelper(ctx, cfg.HelperPath, map[string]any{"v": 1, "operation": "snapshot", "container_id": cfg.ContainerID, "group_ids": cfg.GroupIDs, "max_contacts": cfg.MaxContacts}, &result)
	return result, err
}

func Discover(ctx context.Context, path string) (Discovery, error) {
	var result Discovery
	err := runHelper(ctx, path, map[string]any{"v": 1, "operation": "discover"}, &result)
	if err != nil {
		return Discovery{}, err
	}
	if result.Containers == nil || len(result.Containers) > 100 {
		return Discovery{}, Invalid
	}
	seen := map[string]bool{}
	for _, container := range result.Containers {
		if container.ID == "" || len(container.ID) > 512 || len(container.Name) > 512 || seen[container.ID] || container.Groups == nil || len(container.Groups) > 1000 {
			return Discovery{}, Invalid
		}
		seen[container.ID] = true
		groups := map[string]bool{}
		for _, group := range container.Groups {
			if group.ID == "" || len(group.ID) > 512 || len(group.Name) > 512 || groups[group.ID] {
				return Discovery{}, Invalid
			}
			groups[group.ID] = true
		}
	}
	return result, nil
}
