package module

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Muxcore-Media/core/pkg/contracts"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	"google.golang.org/grpc"
)

type stubSettings struct {
	defs []contracts.SettingDef
	last struct {
		key, value string
	}
}

func (s *stubSettings) Settings() []contracts.SettingDef { return s.defs }

func (s *stubSettings) UpdateSetting(key, value string) error {
	s.last.key, s.last.value = key, value
	return nil
}

type listOnlySettings struct {
	defs []contracts.SettingDef
}

func (s listOnlySettings) Settings() []contracts.SettingDef { return s.defs }

func TestSettingsHandlerFromProvider_ListAndUpdate(t *testing.T) {
	prov := &stubSettings{defs: []contracts.SettingDef{{
		Key: "api_key", Label: "API Key", Type: contracts.SettingTypeSecret, Value: MaskSecret("secret"),
	}}}
	h := SettingsHandlerFromProvider(prov)
	if h.List == nil || h.Update == nil {
		t.Fatal("expected List and Update")
	}
	srv := &settingsMeshServer{moduleID: "mod-a", handler: h}

	listResp, err := srv.Call(context.Background(), &meshv1.CallRequest{
		TargetModule: "mod-a",
		Method:       meshMethodSettings,
	})
	if err != nil || listResp.GetError() != "" {
		t.Fatalf("Settings: err=%v respErr=%q", err, listResp.GetError())
	}
	var got []contracts.SettingDef
	if err := json.Unmarshal(listResp.GetPayload(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "api_key" || got[0].Value != "********" {
		t.Fatalf("unexpected defs: %+v", got)
	}

	raw, _ := json.Marshal(updateSettingPayload{Key: "api_key", Value: "new"})
	upd, err := srv.Call(context.Background(), &meshv1.CallRequest{
		TargetModule: "mod-a",
		Method:       meshMethodUpdateSetting,
		Payload:      raw,
	})
	if err != nil || upd.GetError() != "" {
		t.Fatalf("UpdateSetting: err=%v respErr=%q", err, upd.GetError())
	}
	if prov.last.key != "api_key" || prov.last.value != "new" {
		t.Fatalf("update not applied: %+v", prov.last)
	}
}

func TestSettingsHandlerFromProvider_ListOnly(t *testing.T) {
	h := SettingsHandlerFromProvider(listOnlySettings{})
	if h.List == nil {
		t.Fatal("expected List")
	}
	if h.Update != nil {
		t.Fatal("list-only provider must not wire Update")
	}
	srv := &settingsMeshServer{moduleID: "mod-b", handler: h}
	raw, _ := json.Marshal(updateSettingPayload{Key: "x", Value: "y"})
	resp, err := srv.Call(context.Background(), &meshv1.CallRequest{
		TargetModule: "mod-b",
		Method:       meshMethodUpdateSetting,
		Payload:      raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetError() != "settings update not implemented" {
		t.Fatalf("got %q", resp.GetError())
	}
}

func TestRegisterSettings_RegistersService(t *testing.T) {
	gs := grpc.NewServer()
	RegisterSettings(gs, "mod-c", &stubSettings{})
	info := gs.GetServiceInfo()
	if _, ok := info[meshv1.ModuleMesh_ServiceDesc.ServiceName]; !ok {
		t.Fatalf("ModuleMesh not registered; services=%v", info)
	}
}

func TestMaskSecret(t *testing.T) {
	if MaskSecret("") != "" {
		t.Fatal("empty should stay empty")
	}
	if MaskSecret("abc") != "********" {
		t.Fatal("expected mask")
	}
}
