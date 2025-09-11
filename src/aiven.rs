use std::collections::HashMap;

use anyhow::{bail, Result};
use serde_json::Value;

// all of the reqwest::Clients here should use https://lib.rs/crates/http-cache-reqwest
// as a lot of the requests are done for things like CA and services addresses that are stable and
// shouldn't hit the aiven-api anyway.

pub fn getCa() {}

pub async fn getServiceAddres(
    client: &reqwest::Client,
    project_name: &str,
    service_name: &str,
) -> Result<Value> {
    let url = format!("https://api.aiven.io/v1/project/{project_name}/service/{service_name}");

    let response = match client
        .get(&url)
        .bearer_auth(&cfg.aiven_api_token)
        .send()
        .await
    {
        Ok(r) => r,
        Err(e) => {
            bail!("HTTP request failed with: {:?}", e)
        }
    };
    let response_status = &response.status();
    let Ok(mut response_body) =
        serde_json::from_str::<HashMap<String, serde_json::Value>>(&(response.text().await?))
    else {
        bail!("Unable to parse json returned from: GET {response_status} {url}")
    };
    Ok(response_body)
}
