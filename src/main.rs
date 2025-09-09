use anyhow::Result;
use k8s_openapi::api::{core::v1::Pod, discovery::v1::EndpointSlice};
use kube::{api::{ApiResource, DynamicObject, GroupVersionKind}, runtime::{watcher, WatchStreamExt}, Api};
use std::env;
use kube::kube_runtime::WatchStreamExt;
use futures::{Stream, StreamExt, TryStreamExt};

#[derive(Debug, Clone)]
struct Config {
    aivenToken: String,
    kubernetesWriteRetryInterval: String,
    logFormat: String,
    logLevel: String,
    metricsAddress: String,
    syncPeriod: String,
    mainProject: String,
    projects: String,
}

impl Config {
    fn new() -> Result<Self> {
        Ok(Self {
            aivenToken: env::var("aiven-token")?,
            kubernetesWriteRetryInterval: env::var("kubernetes-write-retry-interval")?,
            logFormat: env::var("log-format")?,
            logLevel: env::var("log-level")?,
            metricsAddress: env::var("metrics-address")?,
            syncPeriod: env::var("sync-period")?,
            mainProject: env::var("main-project")?,
            projects: env::var("projects")?,
        })
    }
}

#[tokio::main]
async fn main() -> Result<()> {
    let _config = Config::new()?;
    let client = kube::client::Client::try_default().await?;
    let wc = watcher::Config::default();
    let api = Api::<DynamicObject>::all_with(client,
        &ApiResource::from_gvk(&GroupVersionKind { group: "aiven.nais.io".to_string(), version: "1".to_string(), kind: "Aiven.Application".to_string() })
    );

    let mut stream = Box::new(watcher(api, wc)
        .default_backoff()
        .applied_objects()
        .boxed());

    while let Some(aivenapp) = stream.try_next().await?  {
        aivenapp

    }
    Ok(())
}
