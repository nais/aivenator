use anyhow::Result;
use futures::{StreamExt, TryStreamExt};
use kube::{
    api::{ApiResource, DynamicObject, GroupVersionKind}, error, runtime::{
        controller::{Action, Controller}, watcher, WatchStreamExt
    }, Api, Client, ResourceExt
};
use tracing::Instrument;
use std::{sync::Arc, time::Duration};
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


async fn reconcile(aiven_application_nais_v1: Arc<DynamicObject>, _ctx: Arc<Ctx>) -> Result<Action, kube::Error> {
    assert_eq!(aiven_application_nais_v1.namespace().is_some(), true);
    let app_namespace = aiven_application_nais_v1.namespace().unwrap();
    let app_name = aiven_application_nais_v1.name_any();
    let aiven_application_v1_spec = aiven_application_nais_v1.data.get("spec".to_owned());
    match aiven_application_v1_spec.unwrap().get("opensearch") {
        Some(a) => tracing::info!("wee {}", a),
        None => tracing::warn!("not opensearch"),
    };

    tracing::info!("reconcile, {} - {}", &app_namespace, &app_name);
    Ok(Action::requeue(Duration::from_secs(200)))
}

fn error_policy(obj: Arc<DynamicObject>, _err: &kube::Error, _ctx: Arc<Ctx>) -> Action {
    tracing::warn!(name=%obj.name_any(), "reconcile failed");
    Action::requeue(Duration::from_secs(200))
}

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    init_tracing()?;
    let client = Client::try_default().await?;

    let gvk = GroupVersionKind {
        group: "aiven.nais.io".into(),
        version: "v1".into(),
        kind: "AivenApplication".into(),
    };
    let ar = ApiResource::from_gvk(&gvk);
    let api = Api::<DynamicObject>::all_with(client.clone(), &ar);

    let ctx = Arc::new(Ctx { client: client.clone() });

    Controller::<DynamicObject>::new_with(api, watcher::Config::default(), ar)
        .run(reconcile, error_policy, ctx)
        .for_each(|res| async move {
            if let Err(err) = res {
                tracing::error!(?err, "reconciliation error");
            }
        })
        .await;

    Ok(())
}

#[derive(Clone)]
struct Ctx {
    client: Client,
    // config: Config,
}
