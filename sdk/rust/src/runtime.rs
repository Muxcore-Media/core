use std::env;
use std::future::Future;
use std::pin::Pin;

use tonic::transport::{Channel, Endpoint};
use tracing::{error, info};

use crate::pb::module_registration_client::ModuleRegistrationClient;
use crate::pb::{ModuleInfo as PbModuleInfo, RegisterRequest, UnregisterRequest};

/// Identity advertised during Register.
#[derive(Clone, Debug, Default)]
pub struct ModuleInfo {
    pub id: String,
    pub name: String,
    pub version: String,
    pub roles: Vec<String>,
    pub description: String,
    pub author: String,
    pub capabilities: Vec<String>,
    pub depends_on: Vec<String>,
    pub min_core_version: String,
    pub http_addr: String,
}

/// Sidecar module contract (mirrors Go contracts.Module).
pub trait Module: Send {
    fn info(&self) -> ModuleInfo;
    fn init<'a>(&'a mut self) -> Pin<Box<dyn Future<Output = Result<(), String>> + Send + 'a>>;
    fn start<'a>(&'a mut self) -> Pin<Box<dyn Future<Output = Result<(), String>> + Send + 'a>>;
    fn stop<'a>(&'a mut self) -> Pin<Box<dyn Future<Output = Result<(), String>> + Send + 'a>>;
}

/// Configuration for [`run`].
///
/// v0.1.0 supports insecure plaintext dial only (set `insecure` or
/// `MUXCORE_INSECURE_DISABLE_TLS`). mTLS lands in a follow-up.
#[derive(Default)]
pub struct RunConfig {
    pub grpc_addr: Option<String>,
    pub module_id: Option<String>,
    pub insecure: bool,
}

fn resolve_addr(cfg: &RunConfig) -> Result<String, String> {
    if let Some(a) = &cfg.grpc_addr {
        if !a.is_empty() {
            return Ok(a.clone());
        }
    }
    for key in ["MUXCORE_GRPC_ADDR", "MUXCORE_MESH_ADDR"] {
        if let Ok(v) = env::var(key) {
            if !v.is_empty() {
                return Ok(v);
            }
        }
    }
    Err(
        "core gRPC address required — set MUXCORE_GRPC_ADDR/MUXCORE_MESH_ADDR or RunConfig.grpc_addr"
            .into(),
    )
}

fn env_insecure() -> bool {
    matches!(
        env::var("MUXCORE_INSECURE_DISABLE_TLS")
            .unwrap_or_default()
            .to_lowercase()
            .as_str(),
        "1" | "true" | "yes"
    )
}

async fn dial(cfg: &RunConfig, addr: &str) -> Result<Channel, String> {
    if !(cfg.insecure || env_insecure()) {
        return Err(
            "Rust SDK v0.1.0 is plaintext-only — set RunConfig.insecure or MUXCORE_INSECURE_DISABLE_TLS=true"
                .into(),
        );
    }
    let endpoint = if addr.starts_with("http://") || addr.starts_with("https://") {
        addr.to_string()
    } else {
        format!("http://{addr}")
    };
    Endpoint::from_shared(endpoint)
        .map_err(|e| e.to_string())?
        .connect()
        .await
        .map_err(|e| e.to_string())
}

/// Register with muxcored, run until SIGINT/SIGTERM, then unregister.
pub async fn run(module: &mut dyn Module, cfg: RunConfig) -> Result<(), String> {
    let addr = resolve_addr(&cfg)?;
    let mut info = module.info();
    let module_id = cfg
        .module_id
        .clone()
        .or_else(|| env::var("MUXCORE_MODULE_ID").ok())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| info.id.clone());
    if module_id.is_empty() {
        return Err("module id required — set MUXCORE_MODULE_ID or ModuleInfo.id".into());
    }
    info.id = module_id.clone();

    let channel = dial(&cfg, &addr).await?;
    let mut client = ModuleRegistrationClient::new(channel);

    let resp = client
        .register(RegisterRequest {
            module_id: module_id.clone(),
            mesh_addr: String::new(),
            min_core_version: info.min_core_version.clone(),
            module_info: Some(PbModuleInfo {
                id: info.id.clone(),
                name: if info.name.is_empty() {
                    module_id.clone()
                } else {
                    info.name.clone()
                },
                version: if info.version.is_empty() {
                    "0.0.0".into()
                } else {
                    info.version.clone()
                },
                roles: info.roles.clone(),
                description: info.description.clone(),
                author: info.author.clone(),
                capabilities: info.capabilities.clone(),
                depends_on: info.depends_on.clone(),
                min_core_version: info.min_core_version.clone(),
                http_addr: info.http_addr.clone(),
            }),
        })
        .await
        .map_err(|e| format!("register RPC: {e}"))?
        .into_inner();

    if !resp.accepted {
        return Err(format!("core rejected registration: {}", resp.error));
    }
    info!(
        id = %module_id,
        version = %info.version,
        mesh = %resp.mesh_addr,
        node = %resp.node_id,
        "registered"
    );

    module.init().await.map_err(|e| format!("init: {e}"))?;
    info!(id = %module_id, "initialized");
    module.start().await.map_err(|e| format!("start: {e}"))?;
    info!(id = %module_id, "started");

    tokio::select! {
        _ = tokio::signal::ctrl_c() => {}
        _ = async {
            let mut sigterm = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                .expect("install SIGTERM handler");
            sigterm.recv().await;
        } => {}
    }

    if let Err(e) = module.stop().await {
        error!(id = %module_id, error = %e, "stop failed");
    }
    if let Err(e) = client
        .unregister(UnregisterRequest {
            module_id: module_id.clone(),
        })
        .await
    {
        error!(id = %module_id, error = %e, "unregister failed");
    }
    info!(id = %module_id, "stopped");
    Ok(())
}

/// Run until `shutdown` is notified instead of OS signals (useful for tests).
pub async fn run_until(
    module: &mut dyn Module,
    cfg: RunConfig,
    shutdown: tokio::sync::oneshot::Receiver<()>,
) -> Result<(), String> {
    let addr = resolve_addr(&cfg)?;
    let mut info = module.info();
    let module_id = cfg
        .module_id
        .clone()
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| info.id.clone());
    info.id = module_id.clone();

    let channel = dial(&cfg, &addr).await?;
    let mut client = ModuleRegistrationClient::new(channel);
    let resp = client
        .register(RegisterRequest {
            module_id: module_id.clone(),
            mesh_addr: String::new(),
            min_core_version: info.min_core_version.clone(),
            module_info: Some(PbModuleInfo {
                id: info.id.clone(),
                name: info.name.clone(),
                version: info.version.clone(),
                roles: info.roles.clone(),
                description: info.description.clone(),
                author: info.author.clone(),
                capabilities: info.capabilities.clone(),
                depends_on: info.depends_on.clone(),
                min_core_version: info.min_core_version.clone(),
                http_addr: info.http_addr.clone(),
            }),
        })
        .await
        .map_err(|e| e.to_string())?
        .into_inner();
    if !resp.accepted {
        return Err(resp.error);
    }
    module.init().await?;
    module.start().await?;
    let _ = shutdown.await;
    let _ = module.stop().await;
    let _ = client
        .unregister(UnregisterRequest {
            module_id: module_id.clone(),
        })
        .await;
    Ok(())
}
