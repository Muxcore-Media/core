package module

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/grpc"

	"github.com/Muxcore-Media/core/pkg/contracts"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
)

const (
	meshMethodSettings      = "Settings"
	meshMethodUpdateSetting = "UpdateSetting"
	maskedSecret            = "********"
)

// SettingsHandler wires admin-ui settings mesh methods onto a module's gRPC server.
type SettingsHandler struct {
	// List returns the module's setting definitions (including current Value).
	List func() []contracts.SettingDef
	// Update applies a setting change. key may be the SettingDef.Key or an env alias.
	Update func(key, value string) error
}

type updateSettingPayload struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type settingsMeshServer struct {
	meshv1.UnimplementedModuleMeshServer
	moduleID string
	handler  SettingsHandler
}

// RegisterMeshHandler registers a ModuleMesh service that serves Settings / UpdateSetting
// for the admin UI (and other mesh callers).
func RegisterMeshHandler(srv *grpc.Server, moduleID string, h SettingsHandler) {
	meshv1.RegisterModuleMeshServer(srv, &settingsMeshServer{
		moduleID: moduleID,
		handler:  h,
	})
}

// MaskSecret returns a fixed mask for non-empty secrets so UIs never echo raw values.
func MaskSecret(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return maskedSecret
}

func (s *settingsMeshServer) Call(ctx context.Context, req *meshv1.CallRequest) (*meshv1.CallResponse, error) {
	if req.GetTargetModule() != "" && req.GetTargetModule() != s.moduleID {
		return &meshv1.CallResponse{Error: fmt.Sprintf("wrong target module %q", req.GetTargetModule())}, nil
	}
	switch req.GetMethod() {
	case meshMethodSettings:
		if s.handler.List == nil {
			return &meshv1.CallResponse{Error: "settings list not implemented"}, nil
		}
		defs := s.handler.List()
		raw, err := json.Marshal(defs)
		if err != nil {
			return &meshv1.CallResponse{Error: err.Error()}, nil
		}
		return &meshv1.CallResponse{Payload: raw}, nil
	case meshMethodUpdateSetting:
		if s.handler.Update == nil {
			return &meshv1.CallResponse{Error: "settings update not implemented"}, nil
		}
		var body updateSettingPayload
		if len(req.GetPayload()) > 0 {
			if err := json.Unmarshal(req.GetPayload(), &body); err != nil {
				return &meshv1.CallResponse{Error: fmt.Sprintf("invalid UpdateSetting payload: %v", err)}, nil
			}
		}
		if body.Key == "" {
			return &meshv1.CallResponse{Error: "UpdateSetting requires Key"}, nil
		}
		if err := s.handler.Update(body.Key, body.Value); err != nil {
			return &meshv1.CallResponse{Error: err.Error()}, nil
		}
		return &meshv1.CallResponse{Payload: []byte(`{"ok":true}`)}, nil
	default:
		return &meshv1.CallResponse{Error: fmt.Sprintf("unknown method %q", req.GetMethod())}, nil
	}
}

func (s *settingsMeshServer) StreamCall(stream meshv1.ModuleMesh_StreamCallServer) error {
	return fmt.Errorf("StreamCall not supported for settings mesh handler")
}
