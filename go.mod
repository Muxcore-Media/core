module github.com/Muxcore-Media/core

go 1.26.3

require (
	github.com/Muxcore-Media/admin-ui v0.0.0
	github.com/Muxcore-Media/api-rest v0.0.0
	github.com/Muxcore-Media/scheduler-cron v0.0.0
	github.com/google/uuid v1.6.0
)

require github.com/robfig/cron/v3 v3.0.1 // indirect

replace github.com/Muxcore-Media/admin-ui => ../modules/admin-ui

replace github.com/Muxcore-Media/api-rest => ../modules/api-rest

replace github.com/Muxcore-Media/scheduler-cron => ../modules/scheduler-cron
