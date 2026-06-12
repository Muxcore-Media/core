package grpcmesh

import (
	"testing"

	discoveryv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/discovery/v1"
	eventsv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/events/v1"
	healthv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/health/v1"
	meshv1 "github.com/Muxcore-Media/core/proto/gen/muxcore/mesh/v1"
	modulev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/module/v1"
	storagev1 "github.com/Muxcore-Media/core/proto/gen/muxcore/storage/v1"
	"google.golang.org/protobuf/proto"
)

func FuzzProtoStorageMessages(f *testing.F) {
	seeds := [][]byte{
		{0x0a, 0x05, 0x68, 0x65, 0x6c, 0x6c, 0x6f},
		{0x10, 0x2a},
		{},
		{0x0a, 0x00, 0x10, 0x00, 0x1a, 0x00},
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var putReq storagev1.PutRequest
		_ = proto.Unmarshal(data, &putReq)

		var putResp storagev1.PutResponse
		_ = proto.Unmarshal(data, &putResp)

		var getReq storagev1.GetRequest
		_ = proto.Unmarshal(data, &getReq)

		var delReq storagev1.DeleteRequest
		_ = proto.Unmarshal(data, &delReq)

		var statReq storagev1.StatRequest
		_ = proto.Unmarshal(data, &statReq)

		var listReq storagev1.ListRequest
		_ = proto.Unmarshal(data, &listReq)

		var capReq storagev1.CapabilitiesRequest
		_ = proto.Unmarshal(data, &capReq)
	})
}

func FuzzProtoHealthMessages(f *testing.F) {
	seeds := [][]byte{
		{0x0a, 0x09, 0x6d, 0x79, 0x73, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65},
		{},
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var checkReq healthv1.HealthCheckRequest
		_ = proto.Unmarshal(data, &checkReq)

		var checkResp healthv1.HealthCheckResponse
		_ = proto.Unmarshal(data, &checkResp)
	})
}

func FuzzProtoEventsMessages(f *testing.F) {
	seeds := [][]byte{
		{0x0a, 0x09, 0x74, 0x65, 0x73, 0x74, 0x2e, 0x65, 0x76, 0x65, 0x6e, 0x74},
		{},
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var evt eventsv1.Event
		_ = proto.Unmarshal(data, &evt)

		var pubReq eventsv1.PublishRequest
		_ = proto.Unmarshal(data, &pubReq)

		var pubResp eventsv1.PublishResponse
		_ = proto.Unmarshal(data, &pubResp)

		var subReq eventsv1.SubscribeRequest
		_ = proto.Unmarshal(data, &subReq)

		var reqEvt eventsv1.RequestEvent
		_ = proto.Unmarshal(data, &reqEvt)

		var replayReq eventsv1.ReplayRequest
		_ = proto.Unmarshal(data, &replayReq)
	})
}

func FuzzProtoDiscoveryMessages(f *testing.F) {
	seeds := [][]byte{
		{0x0a, 0x08, 0x6e, 0x6f, 0x64, 0x65, 0x2d, 0x6f, 0x6e, 0x65},
		{},
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var nodeInfo discoveryv1.NodeInfo
		_ = proto.Unmarshal(data, &nodeInfo)

		var joinReq discoveryv1.JoinRequest
		_ = proto.Unmarshal(data, &joinReq)

		var joinResp discoveryv1.JoinResponse
		_ = proto.Unmarshal(data, &joinResp)

		var leaveReq discoveryv1.LeaveRequest
		_ = proto.Unmarshal(data, &leaveReq)

		var hbReq discoveryv1.HeartbeatRequest
		_ = proto.Unmarshal(data, &hbReq)

		var hbResp discoveryv1.HeartbeatResponse
		_ = proto.Unmarshal(data, &hbResp)

		var membersReq discoveryv1.MembersRequest
		_ = proto.Unmarshal(data, &membersReq)

		var membersResp discoveryv1.MembersResponse
		_ = proto.Unmarshal(data, &membersResp)

		var findByCapReq discoveryv1.FindByCapabilityRequest
		_ = proto.Unmarshal(data, &findByCapReq)

		var findByRoleReq discoveryv1.FindByRoleRequest
		_ = proto.Unmarshal(data, &findByRoleReq)

		var resolveReq discoveryv1.ResolveRequest
		_ = proto.Unmarshal(data, &resolveReq)

		var resolveResp discoveryv1.ResolveResponse
		_ = proto.Unmarshal(data, &resolveResp)
	})
}

func FuzzProtoMeshMessages(f *testing.F) {
	seeds := [][]byte{
		{0x0a, 0x0a, 0x74, 0x61, 0x72, 0x67, 0x65, 0x74, 0x2d, 0x6d, 0x6f, 0x64},
		{},
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var callReq meshv1.CallRequest
		_ = proto.Unmarshal(data, &callReq)

		var callResp meshv1.CallResponse
		_ = proto.Unmarshal(data, &callResp)
	})
}

func FuzzProtoModuleRegistrationMessages(f *testing.F) {
	seeds := [][]byte{
		{0x0a, 0x08, 0x6d, 0x79, 0x2d, 0x6d, 0x6f, 0x64, 0x75, 0x6c, 0x65},
		{},
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var regReq modulev1.RegisterRequest
		_ = proto.Unmarshal(data, &regReq)

		var regResp modulev1.RegisterResponse
		_ = proto.Unmarshal(data, &regResp)

		var unregReq modulev1.UnregisterRequest
		_ = proto.Unmarshal(data, &unregReq)

		var unregResp modulev1.UnregisterResponse
		_ = proto.Unmarshal(data, &unregResp)
	})
}
