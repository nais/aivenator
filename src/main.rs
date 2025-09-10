use anyhow::Result;
use futures::StreamExt;
use k8s_openapi::serde_json::{self, Map, Value};
use kube::{
    Api, Client, Resource, ResourceExt,
    api::{ApiResource, DynamicObject, GroupVersionKind, Object},
    runtime::{
        controller::{Action, Controller},
        watcher,
    },
};
use std::{sync::Arc, time::Duration};
use tracing::{Level, instrument, span};
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

#[instrument(skip(opensearch_spec))]
fn reconcile_opensearch(
    opensearch_spec: &Map<String, serde_json::Value>,
    app_name: &str,
    app_namespace: &str,
) -> Result<Action, kube::Error> {
    // let opensearch_name = opensearch_spec.;
    let span = span!(Level::INFO, "opensearch");
    let _enter = span.enter();
    tracing::info!(message = "reconciling opensearch", ?opensearch_spec);

    Ok(Action::requeue(Duration::from_secs(200)))
}

async fn reconcile(
    aiven_application_nais_v1: Arc<DynamicObject>,
    _ctx: Arc<Ctx>,
) -> Result<Action, kube::Error> {
    let metadata = aiven_application_nais_v1.meta();
    let Some(app_namespace) = aiven_application_nais_v1.namespace() else {
        tracing::warn!("namespace missing from aivenapp: {:#?}", metadata);
        return Ok(Action::requeue(Duration::from_secs(200)));
    };
    let app_name = aiven_application_nais_v1.name_any();

    // Configure log fields
    let span = span!(Level::INFO, "reconciler", ?app_name, ?app_namespace);
    let _enter = span.enter();
    tracing::info!("in reconcile()");

    let Some(aiven_application_v1_spec) = aiven_application_nais_v1.data.get("spec".to_owned())
    else {
        tracing::warn!(message = "no `spec` found", ?app_namespace, ?app_name);
        return Ok(Action::requeue(Duration::from_secs(200)));
    };

    let mut action = Action::requeue(Duration::from_secs(200));
    // Check for the interesting subsets of `spec`
    if let Some(opensearch) = aiven_application_v1_spec.get("openSearch")
        && let Some(opensearch_spec) = opensearch.as_object()
    {
        action = reconcile_opensearch(&opensearch_spec, &app_name, &app_namespace)?;
    } else {
        tracing::warn!(message = "no opensearch in spec", ?app_name, ?app_namespace);
    }
    match aiven_application_v1_spec.get("openSearch") {
        Some(opensearch) => {
            tracing::info!(message = "opensearch spec found", ?opensearch);
            action = reconcile_opensearch(&opensearch.as_object(), &app_name, &app_namespace)?;
        }
        None => tracing::warn!(message = "no opensearch in spec", ?app_name, ?app_namespace),
    };
    match aiven_application_v1_spec.get("kafka") {
        Some(kafka) => tracing::info!(message = "kafka spec found", ?kafka),
        None => tracing::warn!(message = "no kafka in spec", ?app_name, ?app_namespace),
    };
    match aiven_application_v1_spec.get("valkey") {
        Some(valkey) => {
            tracing::info!(message = "valkey(s) spec found", ?valkey)
        }
        None => tracing::warn!(message = "no valkey in spec", ?app_name, ?app_namespace),
    };

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

    let ctx = Arc::new(Ctx {
        client: client.clone(),
    });

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
