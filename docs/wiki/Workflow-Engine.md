# Workflow Engine

## Overview

The workflow engine is arguably the **core product** of MuxCore. It orchestrates multi-step pipelines across modules without implementing any step directly.

## Why a Workflow Engine?

Existing \*arr stacks have implicit, hardcoded workflows:
```
Sonarr: Search → Download → Move → Rename → Notify
```

MuxCore makes workflows **explicit, configurable, and extensible**:
```
User defines: Request → Metadata → Search → Download → Verify → Extract → Analyze → Transcode → Supplementary Content → Import → Notify
```

## Workflow Definition

```go
type WorkflowDefinition struct {
    ID    string
    Name  string
    Steps []WorkflowStep
}

type WorkflowStep struct {
    Name    string         // Human-readable step name
    Handler string         // Module that handles this step
    Input   map[string]any // Parameters for this step
    Retry   int            // Max retry attempts
    Timeout int            // Timeout in seconds
}

// Advanced workflow features (conditional steps, parallel execution, compensation) planned for Phase 2.
```

### Example Workflow: Movie Import

```yaml
id: movie-request
name: "Movie Request Pipeline"
steps:
  - name: metadata-lookup
    handler: metadata-tmdb
    retry: 3
    timeout: 30

  - name: indexer-search
    handler: indexer-prowlarr
    retry: 2
    timeout: 60

  - name: download
    handler: downloader-qbittorrent
    retry: 1
    timeout: 0  # no timeout, download may take hours

  - name: verify
    handler: verifier-builtin
    retry: 3
    timeout: 300

  - name: extract
    handler: extractor-unpackerr
    retry: 2
    timeout: 600

  - name: analyze
    handler: analyzer-builtin
    retry: 1
    timeout: 120

  - name: transcode
    handler: transcoder-ffmpeg
    retry: 1
    timeout: 3600

  - name: content-fetch
    handler: content-subtitle  # kind "subtitle" — also works for "lyrics" with a different handler
    retry: 2
    timeout: 120

  - name: library-import
    handler: media-movies
    retry: 1
    timeout: 60

  - name: notify
    handler: notifier-discord
    retry: 2
    timeout: 10
```

## Key Features

### Retries with Backoff
```
Attempt 1: immediate
Attempt 2: 5s delay
Attempt 3: 25s delay
Attempt 4: 125s delay (exponential backoff)
```

### Idempotency
The same request processed twice should not result in double downloads, double imports, etc. Idempotency keys are generated from the request and tracked.

```go
idempotencyKey := hash(mediaType + imdbID + quality + season + episode)
```

### Advanced Features *(planned for Phase 2)*

Advanced workflow features including compensation (undoing partial work on failure), conditional step execution, parallel step execution, and fallback handler chains are planned for Phase 2.

## Workflow Execution

```go
type WorkflowEngine interface {
    Define(ctx, WorkflowDefinition) error
    Run(ctx, workflowID string, params map[string]any) (string, error)
    Status(ctx, runID string) (WorkflowRun, error)
    Cancel(ctx, runID string) error
}
```

## Built-In Workflows

MuxCore ships with sensible default workflows:

| Workflow | Description |
|----------|-------------|
| `movie-request` *(planned)* | Full movie acquisition pipeline |
| `tv-request` *(planned)* | TV episode acquisition |
| `music-request` *(planned)* | Music album acquisition |
| `book-request` *(planned)* | Book acquisition |
| `media-transcode` *(planned)* | Transcoding pipeline |
| `media-migrate` *(planned)* | Move media between storage providers |
| `media-backup` *(planned)* | Backup to cold storage |
| `library-scan` *(planned)* | Scan and import existing media |

Built-in workflows are planned. In Phase 1, workflows must be defined manually.

## Power Use Cases

### Intelligent Orchestration
- **Move workload to idle GPU** — Transcoding scheduler picks the least-loaded GPU worker
- **Auto-balance storage** — Move media to the provider with most free space
- **Prioritize by popularity** — Recently watched media stays on fast storage
- **Predictive pre-transcoding** — Transcode next episode while watching current

### Cross-Media Workflows
```
User adds an anime series
  → Checks for manga adaptation
  → Checks for light novel source
  → Offers to track and download all related media
```

This cross-media awareness is impossible in siloed \*arr stacks.
