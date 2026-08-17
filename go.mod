go 1.26.4

require (
	github.com/Muxcore-Media/contracts-reconciler v0.0.0-20260526214139-5692629c5d6e
	github.com/google/uuid v1.6.0
	google.golang.org/grpc v1.83.0
	google.golang.org/protobuf v1.36.12
)

require (
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
)

require (
	github.com/Muxcore-Media/core/pkg/contracts v0.0.0
	golang.org/x/sys v0.47.0
)

replace github.com/Muxcore-Media/core/pkg/contracts => ./pkg/contracts

module github.com/Muxcore-Media/core
