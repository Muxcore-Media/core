//! MuxCore Rust module SDK — register sidecars with muxcored.

mod runtime;

pub use runtime::{run, run_until, Module, ModuleInfo, RunConfig};

pub mod pb {
    tonic::include_proto!("muxcore.module.v1");
}
