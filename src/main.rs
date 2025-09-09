use anyhow::Result;
use futures::{StreamExt, TryStreamExt};
use kube::{
    Api,
    api::{ApiResource, DynamicObject, GroupVersionKind},
    runtime::{WatchStreamExt, watcher},
};
use tracing_subscriber::filter::LevelFilter;
use tracing_subscriber::layer::SubscriberExt;
use tracing_subscriber::{EnvFilter, util::SubscriberInitExt};

use std::{env, io::IsTerminal};

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
    let (json_fmt, std_fmt) = match std::io::stdout().is_terminal() {
        true => (None, Some(tracing_subscriber::fmt::layer())),
        false => (
            Some(tracing_subscriber::fmt::layer().json().flatten_event(true)),
            None,
        ),
    };
    let env_filter = EnvFilter::builder()
        .with_default_directive(LevelFilter::INFO.into())
        .from_env_lossy();

    tracing_subscriber::registry()
        .with(env_filter)
        .with(json_fmt)
        .with(std_fmt)
        .init();
    Ok(())
}

#[tokio::main]
async fn main() -> Result<()> {
    let _ = init_tracing()?;
    // let _config = Config::new()?;
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
