# MuxCore Rust SDK

Scaffold for language-agnostic module SDKs (§4.1) — **Rust** `v0.1.0` (with Go / Python / TypeScript).

Uses `tonic` + vendored `ModuleRegistration` proto (`build.rs`).

## Build / test

```bash
cd core/sdk/rust
cargo test
```

## Quick start

```rust
use muxcore_sdk::{run, Module, ModuleInfo, RunConfig};
use std::future::Future;
use std::pin::Pin;

struct Echo;

impl Module for Echo {
    fn info(&self) -> ModuleInfo {
        ModuleInfo {
            id: "echo-rs".into(),
            name: "echo-rs".into(),
            version: "0.1.0".into(),
            roles: vec!["tool".into()],
            ..Default::default()
        }
    }
    fn init<'a>(&'a mut self) -> Pin<Box<dyn Future<Output = Result<(), String>> + Send + 'a>> {
        Box::pin(async { Ok(()) })
    }
    fn start<'a>(&'a mut self) -> Pin<Box<dyn Future<Output = Result<(), String>> + Send + 'a>> {
        Box::pin(async { Ok(()) })
    }
    fn stop<'a>(&'a mut self) -> Pin<Box<dyn Future<Output = Result<(), String>> + Send + 'a>> {
        Box::pin(async { Ok(()) })
    }
}

#[tokio::main]
async fn main() -> Result<(), String> {
    let mut m = Echo;
    run(
        &mut m,
        RunConfig {
            insecure: true,
            ..Default::default()
        },
    )
    .await
}
```

```bash
MUXCORE_GRPC_ADDR=127.0.0.1:9090 MUXCORE_INSECURE_DISABLE_TLS=true cargo run --example echo
```

## Out of scope

- Discovery / Storage / Events client parity with Go
- Settings mesh
