module github.com/Muxcore-Media/core/sdk/go/client

go 1.26.4

require (
	github.com/Muxcore-Media/core v0.5.8
	google.golang.org/grpc v1.82.1
)

require (
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/Muxcore-Media/core => ../../..

replace github.com/Muxcore-Media/core/pkg/contracts => ../../../pkg/contracts
