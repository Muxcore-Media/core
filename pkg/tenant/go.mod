module github.com/Muxcore-Media/core/pkg/tenant

go 1.26.4

require github.com/Muxcore-Media/core/pkg/contracts v0.5.8

require github.com/Muxcore-Media/contracts-media v0.1.0 // indirect

replace github.com/Muxcore-Media/core/pkg/contracts => ../contracts

replace github.com/Muxcore-Media/contracts-media => ../../../contracts-media
