go 1.26.6

require (
	github.com/Muxcore-Media/contracts-reconciler v0.0.0-20260526214139-5692629c5d6e
	github.com/google/uuid v1.6.0
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
)

require (
	github.com/Muxcore-Media/core/pkg/contracts v0.6.0
	golang.org/x/crypto v0.57.0
	golang.org/x/sys v0.48.0
)

replace github.com/Muxcore-Media/core/pkg/contracts => ./pkg/contracts

module github.com/Muxcore-Media/core
