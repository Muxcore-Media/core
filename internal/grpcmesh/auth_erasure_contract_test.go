package grpcmesh

import (
	"context"
	"net"
	"testing"
	"time"

	authv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/auth/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// ADR-0035 erasure ledger compatibility fixtures. They mirror the ADR-0026
// session-contract fixtures: providers built before the ledger (auth-oidc, and
// auth-local until E2) must keep working, and must answer the ledger RPCs with
// Unimplemented so consumers can report "unsupported" instead of guessing.

var erasureMethods = map[string]bool{
	"ListUserErasures":     true,
	"AckUserErasure":       true,
	"GetUserErasureStatus": true,
}

// legacyErasureAuthProvider is a provider that predates ADR-0035: it embeds
// UnimplementedAuthServiceServer and implements DeleteUser the old way, so it
// never sets erasure_id.
type legacyErasureAuthProvider struct {
	authv1.UnimplementedAuthServiceServer
}

var _ authv1.AuthServiceServer = (*legacyErasureAuthProvider)(nil)

func (legacyErasureAuthProvider) Validate(_ context.Context, req *authv1.ValidateRequest) (*authv1.ValidateResponse, error) {
	return &authv1.ValidateResponse{Valid: req.GetToken() == "admin-session", UserId: "admin-user"}, nil
}

func (legacyErasureAuthProvider) DeleteUser(_ context.Context, req *authv1.DeleteUserRequest) (*authv1.DeleteUserResponse, error) {
	if req.GetUserId() != "victim" {
		return &authv1.DeleteUserResponse{Error: "user not found"}, nil
	}
	return &authv1.DeleteUserResponse{}, nil
}

func dialBufconnAuth(t *testing.T, register func(*grpc.Server)) authv1.AuthServiceClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	register(server)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-served; err != nil {
			t.Errorf("serve auth provider: %v", err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///auth-provider",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close auth client: %v", err)
		}
	})
	return authv1.NewAuthServiceClient(conn)
}

func TestAuthErasureContractBackwardCompatibility(t *testing.T) {
	for _, oldDescriptor := range []bool{false, true} {
		name := "rebuilt_provider_with_unimplemented_methods"
		if oldDescriptor {
			name = "provider_running_previous_service_descriptor"
		}
		t.Run(name, func(t *testing.T) {
			provider := &legacyErasureAuthProvider{}
			client := dialBufconnAuth(t, func(server *grpc.Server) {
				if !oldDescriptor {
					authv1.RegisterAuthServiceServer(server, provider)
					return
				}
				desc := authv1.AuthService_ServiceDesc
				desc.Methods = nil
				for _, method := range authv1.AuthService_ServiceDesc.Methods {
					if !erasureMethods[method.MethodName] {
						desc.Methods = append(desc.Methods, method)
					}
				}
				server.RegisterService(&desc, provider)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			validated, err := client.Validate(ctx, &authv1.ValidateRequest{Token: "admin-session"})
			if err != nil || !validated.GetValid() {
				t.Fatalf("existing Validate contract: response=%v error=%v", validated, err)
			}
			deleted, err := client.DeleteUser(ctx, &authv1.DeleteUserRequest{UserId: "victim"})
			if err != nil || deleted.GetError() != "" {
				t.Fatalf("existing DeleteUser contract: response=%v error=%v", deleted, err)
			}
			if deleted.GetErasureId() != "" {
				t.Fatalf("legacy provider produced erasure_id %q; must be empty", deleted.GetErasureId())
			}
			if _, err := client.ListUserErasures(ctx, &authv1.ListUserErasuresRequest{PageSize: 100}); status.Code(err) != codes.Unimplemented {
				t.Fatalf("unsupported ListUserErasures: got %v, want Unimplemented", err)
			}
			if _, err := client.AckUserErasure(ctx, &authv1.AckUserErasureRequest{
				ErasureId: "e-1", Outcome: authv1.ErasureOutcome_ERASURE_OUTCOME_OK,
			}); status.Code(err) != codes.Unimplemented {
				t.Fatalf("unsupported AckUserErasure: got %v, want Unimplemented", err)
			}
			if _, err := client.GetUserErasureStatus(ctx, &authv1.GetUserErasureStatusRequest{PendingOnly: true}); status.Code(err) != codes.Unimplemented {
				t.Fatalf("unsupported GetUserErasureStatus: got %v, want Unimplemented", err)
			}
		})
	}
}

// legacyDeleteUserResponse builds the DeleteUserResponse descriptor exactly as
// it was before ADR-0035 (only `string error = 1`), standing in for a client
// compiled against the previous proto.
func legacyDeleteUserResponse(t *testing.T) protoreflect.MessageDescriptor {
	t.Helper()
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("legacy/muxcore/auth/v1/auth.proto"),
		Package: proto.String("legacy.muxcore.auth.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("DeleteUserResponse"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("error"),
				JsonName: proto.String("error"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			}},
		}},
	}
	fd, err := protodesc.NewFile(fdp, nil)
	if err != nil {
		t.Fatal(err)
	}
	return fd.Messages().ByName("DeleteUserResponse")
}

func TestDeleteUserResponseErasureIDWireCompatibility(t *testing.T) {
	legacy := legacyDeleteUserResponse(t)

	t.Run("old_client_ignores_erasure_id", func(t *testing.T) {
		wire, err := proto.Marshal(&authv1.DeleteUserResponse{ErasureId: "erasure-123"})
		if err != nil {
			t.Fatal(err)
		}
		old := dynamicpb.NewMessage(legacy)
		if err := proto.Unmarshal(wire, old); err != nil {
			t.Fatalf("old client cannot decode new response: %v", err)
		}
		if got := old.Get(legacy.Fields().ByNumber(1)).String(); got != "" {
			t.Fatalf("old client error field = %q, want empty (success)", got)
		}
		if len(old.GetUnknown()) == 0 {
			t.Fatal("erasure_id should be preserved as an unknown field by the old client")
		}
		// A proxy built on the old descriptor must forward the field intact.
		forwarded, err := proto.Marshal(old)
		if err != nil {
			t.Fatal(err)
		}
		var again authv1.DeleteUserResponse
		if err := proto.Unmarshal(forwarded, &again); err != nil || again.GetErasureId() != "erasure-123" {
			t.Fatalf("round trip through old descriptor: id=%q err=%v", again.GetErasureId(), err)
		}
	})

	t.Run("old_provider_response_has_empty_erasure_id", func(t *testing.T) {
		old := dynamicpb.NewMessage(legacy)
		old.Set(legacy.Fields().ByNumber(1), protoreflect.ValueOfString("user not found"))
		wire, err := proto.Marshal(old)
		if err != nil {
			t.Fatal(err)
		}
		var resp authv1.DeleteUserResponse
		if err := proto.Unmarshal(wire, &resp); err != nil {
			t.Fatal(err)
		}
		if resp.GetError() != "user not found" || resp.GetErasureId() != "" {
			t.Fatalf("decoded old provider response = %+v", &resp)
		}
	})
}

// TestErasureContractFieldNumbers freezes the ADR-0035 wire contract: field
// numbers and enum values are never renumbered or reused.
func TestErasureContractFieldNumbers(t *testing.T) {
	want := map[string]map[string]protoreflect.FieldNumber{
		"DeleteUserResponse":           {"error": 1, "erasure_id": 2},
		"ListUserErasuresRequest":      {"page_token": 1, "page_size": 2},
		"UserErasure":                  {"erasure_id": 1, "user_id": 2, "tenant_id": 3, "deleted_at": 4, "acknowledged_by_caller": 5},
		"ListUserErasuresResponse":     {"erasures": 1, "next_page_token": 2},
		"AckUserErasureRequest":        {"erasure_id": 1, "outcome": 2, "detail_code": 3, "counts": 4},
		"AckUserErasureResponse":       {},
		"GetUserErasureStatusRequest":  {"erasure_id": 1, "pending_only": 2, "page_size": 3, "page_token": 4},
		"ErasureModuleStatus":          {"module_id": 1, "outcome": 2, "detail_code": 3, "acked_at": 4, "required": 5},
		"ErasureStatus":                {"erasure_id": 1, "deleted_at": 2, "modules": 3, "complete": 4},
		"GetUserErasureStatusResponse": {"erasures": 1, "next_page_token": 2},
	}
	file := authv1.File_muxcore_auth_v1_auth_proto
	for msgName, fields := range want {
		md := file.Messages().ByName(protoreflect.Name(msgName))
		if md == nil {
			t.Errorf("message %s missing", msgName)
			continue
		}
		if md.Fields().Len() != len(fields) {
			t.Errorf("%s has %d fields, want %d", msgName, md.Fields().Len(), len(fields))
		}
		for name, num := range fields {
			fd := md.Fields().ByName(protoreflect.Name(name))
			if fd == nil || fd.Number() != num {
				t.Errorf("%s.%s: got %v, want field number %d", msgName, name, fd, num)
			}
		}
	}
	outcome := file.Enums().ByName("ErasureOutcome")
	for name, num := range map[string]protoreflect.EnumNumber{
		"ERASURE_OUTCOME_UNSPECIFIED": 0, "ERASURE_OUTCOME_OK": 1,
		"ERASURE_OUTCOME_FAILED": 2, "ERASURE_OUTCOME_UNSUPPORTED": 3,
	} {
		if v := outcome.Values().ByName(protoreflect.Name(name)); v == nil || v.Number() != num {
			t.Errorf("ErasureOutcome.%s: got %v, want %d", name, v, num)
		}
	}
	svc := file.Services().ByName("AuthService")
	for name, io := range map[string][2]string{
		"ListUserErasures":     {"ListUserErasuresRequest", "ListUserErasuresResponse"},
		"AckUserErasure":       {"AckUserErasureRequest", "AckUserErasureResponse"},
		"GetUserErasureStatus": {"GetUserErasureStatusRequest", "GetUserErasureStatusResponse"},
	} {
		m := svc.Methods().ByName(protoreflect.Name(name))
		if m == nil || string(m.Input().Name()) != io[0] || string(m.Output().Name()) != io[1] || m.IsStreamingClient() || m.IsStreamingServer() {
			t.Errorf("AuthService.%s signature changed: %v", name, m)
		}
	}
}
