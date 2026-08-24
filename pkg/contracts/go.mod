module github.com/Muxcore-Media/core/pkg/contracts

go 1.26.4

require (
	github.com/Muxcore-Media/contracts-media v0.1.0
	github.com/Muxcore-Media/contracts-scanner v0.1.0
	github.com/Muxcore-Media/contracts-automation v0.1.0
	github.com/Muxcore-Media/contracts-metadata v0.1.0
)

// Monorepo dev: sibling replaces. Release branches drop these and pin tagged modules only.
replace (
	github.com/Muxcore-Media/contracts-media => ../../../contracts-media
	github.com/Muxcore-Media/contracts-scanner => ../../../contracts-scanner
	github.com/Muxcore-Media/contracts-automation => ../../../contracts-automation
	github.com/Muxcore-Media/contracts-metadata => ../../../contracts-metadata
)
