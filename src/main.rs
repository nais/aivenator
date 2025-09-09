use anyhow::Result;
use futures::{StreamExt, TryStreamExt};
use kube::{
    api::{ApiResource, DynamicObject, GroupVersionKind},
    runtime::{watcher, WatchStreamExt},
    Api,
};
use std::env;
use tracing_subscriber::util::SubscriberInitExt;

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

fn init_tracing() -> Result<()> {
    tracing_subscriber::registry().init();
    Ok(())
}

#[tokio::main]
async fn main() -> Result<()> {
    let _ = init_tracing()?;
    //    let _config = Config::new()?;
    tracing::info!("client");
    let client = kube::client::Client::try_default().await?;
    tracing::info!("wc");

    let wc = watcher::Config::default();

    tracing::info!("started api");

    let api = Api::<DynamicObject>::all_with(
        client,
        &ApiResource::from_gvk(&GroupVersionKind {
            group: "aiven.nais.io".to_string(),
            version: "v1".to_string(),
            kind: "AivenApplication".to_string(),
        }),
    );

    tracing::info!("make stream");

    let mut stream = Box::new(
        watcher(api, wc)
            .default_backoff()
            .applied_objects()
            .map_err(anyhow::Error::from)
            .boxed(),
    );

    tracing::info!("started watcher");
    while let Some(aivenapp) = stream.try_next().await? {
        tracing::info!("foo {}", aivenapp.metadata.name.unwrap())
    }
    Ok(())
}
