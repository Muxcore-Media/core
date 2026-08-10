use std::future::Future;
use std::net::SocketAddr;
use std::pin::Pin;
use std::sync::{Arc, Mutex};

use tokio::sync::oneshot;
use tonic::{transport::Server, Request, Response, Status};

use muxcore_sdk::pb::module_registration_server::{ModuleRegistration, ModuleRegistrationServer};
use muxcore_sdk::pb::{
    BootstrapRegisterRequest, BootstrapRegisterResponse, RegisterRequest, RegisterResponse,
    UnregisterRequest, UnregisterResponse,
};
use muxcore_sdk::{run_until, Module, ModuleInfo, RunConfig};

#[derive(Default, Clone)]
struct FakeReg {
    registered: Arc<Mutex<Vec<String>>>,
    unregistered: Arc<Mutex<Vec<String>>>,
}

#[tonic::async_trait]
impl ModuleRegistration for FakeReg {
    async fn register(
        &self,
        request: Request<RegisterRequest>,
    ) -> Result<Response<RegisterResponse>, Status> {
        let id = request.into_inner().module_id;
        self.registered.lock().unwrap().push(id);
        Ok(Response::new(RegisterResponse {
            accepted: true,
            error: String::new(),
            mesh_addr: "127.0.0.1:9090".into(),
            node_id: "node-1".into(),
        }))
    }

    async fn unregister(
        &self,
        request: Request<UnregisterRequest>,
    ) -> Result<Response<UnregisterResponse>, Status> {
        let id = request.into_inner().module_id;
        self.unregistered.lock().unwrap().push(id);
        Ok(Response::new(UnregisterResponse { acknowledged: true }))
    }

    async fn bootstrap_register(
        &self,
        _request: Request<BootstrapRegisterRequest>,
    ) -> Result<Response<BootstrapRegisterResponse>, Status> {
        Ok(Response::new(BootstrapRegisterResponse {
            accepted: false,
            error: "unused".into(),
            signed_cert: String::new(),
            key_pem: String::new(),
            ca_cert: String::new(),
        }))
    }
}

struct Demo;

impl Module for Demo {
    fn info(&self) -> ModuleInfo {
        ModuleInfo {
            id: "demo-rs".into(),
            name: "demo-rs".into(),
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

#[tokio::test]
async fn registers_and_unregisters() {
    let svc = FakeReg::default();
    let registered = svc.registered.clone();
    let unregistered = svc.unregistered.clone();

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr: SocketAddr = listener.local_addr().unwrap();
    let incoming = tokio_stream::wrappers::TcpListenerStream::new(listener);

    let server = tokio::spawn(async move {
        Server::builder()
            .add_service(ModuleRegistrationServer::new(svc))
            .serve_with_incoming(incoming)
            .await
            .unwrap();
    });

    let (tx, rx) = oneshot::channel();
    let mut demo = Demo;
    let join = tokio::spawn(async move {
        run_until(
            &mut demo,
            RunConfig {
                grpc_addr: Some(addr.to_string()),
                insecure: true,
                ..Default::default()
            },
            rx,
        )
        .await
    });

    tokio::time::sleep(std::time::Duration::from_millis(100)).await;
    let _ = tx.send(());
    join.await.unwrap().unwrap();
    server.abort();

    assert_eq!(*registered.lock().unwrap(), vec!["demo-rs".to_string()]);
    assert_eq!(*unregistered.lock().unwrap(), vec!["demo-rs".to_string()]);
}
