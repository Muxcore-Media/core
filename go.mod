module github.com/Muxcore-Media/core

go 1.26.3

require (
	github.com/Muxcore-Media/admin-ui v0.0.0
	github.com/Muxcore-Media/api-rest v0.0.0
	github.com/Muxcore-Media/cluster-gossip v0.0.0
	github.com/Muxcore-Media/scheduler-cron v0.0.0
	github.com/google/uuid v1.6.0
	google.golang.org/grpc v1.81.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/robfig/cron/v3 v3.0.1 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
)

replace github.com/Muxcore-Media/admin-ui => ../modules/admin-ui

replace github.com/Muxcore-Media/api-rest => ../modules/api-rest

replace github.com/Muxcore-Media/cluster-gossip => ../modules/cluster-gossip

replace github.com/Muxcore-Media/scheduler-cron => ../modules/scheduler-cron
