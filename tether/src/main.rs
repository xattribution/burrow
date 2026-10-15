//! burrow-tether: reach Burrow from anywhere without port forwarding.
//!
//! `serve` runs next to Burrow (Burrow starts it when an admin turns the feature on).
//! It forwards trusted peers' streams to Burrow's tether port, which serves what the
//! public port does: the phone channel and share links. Burrow drives it over stdin
//! and stdout, one JSON object per line, so there is no control port to protect.
//!
//! `connect` is the other end for a computer: it joins with an invite (read from
//! stdin, never the command line, since an invite is a credential) and prints the
//! loopback port that leads to Burrow.
//!
//! Invites are never logged. Logs go to stderr.

use std::{net::SocketAddr, path::PathBuf, sync::Arc, time::Duration};

use anyhow::{Context, Result, anyhow, bail};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use tethersys::{Config, Discovery, LinkKind, Node, NodeId, Profile};
use tokio::{
    io::{AsyncBufReadExt, AsyncWriteExt, BufReader},
    sync::Mutex,
};

const USAGE: &str = "usage:
  burrow-tether serve --target 127.0.0.1:PORT --data DIR [--port UDP] [--local]
  burrow-tether connect --data DIR [--listen 127.0.0.1:0] [--local]
      then write {\"invite\":\"...\"} or {\"peer\":\"<id>\"} on stdin";

struct Args {
    cmd: String,
    target: Option<SocketAddr>,
    data: PathBuf,
    port: Option<u16>,
    listen: SocketAddr,
    local: bool,
}

fn args() -> Result<Args> {
    let mut it = std::env::args().skip(1);
    let cmd = it.next().ok_or_else(|| anyhow!(USAGE))?;
    let mut a = Args {
        cmd,
        target: None,
        data: PathBuf::new(),
        port: None,
        listen: SocketAddr::from(([127, 0, 0, 1], 0)),
        local: false,
    };
    while let Some(k) = it.next() {
        let mut v = || it.next().ok_or_else(|| anyhow!("{k} needs a value"));
        match k.as_str() {
            "--target" => a.target = Some(v()?.parse().context("--target")?),
            "--data" => a.data = PathBuf::from(v()?),
            "--port" => a.port = Some(v()?.parse().context("--port")?),
            "--listen" => a.listen = v()?.parse().context("--listen")?,
            "--local" => a.local = true,
            _ => bail!("unknown argument {k}\n{USAGE}"),
        }
    }
    if a.data.as_os_str().is_empty() {
        bail!("--data is required\n{USAGE}");
    }
    Ok(a)
}

/// Public: relays, DNS, DHT and LAN discovery. `--local` is for tests on one machine:
/// no relays and no discovery, the invite carries the addresses.
fn config(name: &str, a: &Args) -> Config {
    let profile = if a.local {
        Profile::Local
    } else {
        Profile::Public
    };
    let mut cfg = Config::new(name, profile).data_dir(&a.data);
    if a.local {
        cfg.discovery = Discovery {
            n0_dns: false,
            mdns: false,
            dht: false,
        };
    }
    cfg.port = a.port;
    cfg
}

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_env("BURROW_TETHER_LOG")
                .unwrap_or_else(|_| "warn".into()),
        )
        .init();
    let res = match args() {
        Ok(a) if a.cmd == "serve" => serve(a).await,
        Ok(a) if a.cmd == "connect" => connect(a).await,
        Ok(a) => Err(anyhow!("unknown command {}\n{USAGE}", a.cmd)),
        Err(e) => Err(e),
    };
    if let Err(e) = res {
        eprintln!("burrow-tether: {e:#}");
        std::process::exit(1);
    }
}

#[derive(Deserialize)]
struct Cmd {
    #[serde(default)]
    id: u64,
    op: String,
    #[serde(default)]
    tag: String,
    #[serde(default)]
    ttl: u64,
}

#[derive(Serialize)]
struct PeerOut {
    id: String,
    name: String,
    tags: Vec<String>,
    link: Option<&'static str>,
}

fn link_name(l: LinkKind) -> &'static str {
    match l {
        LinkKind::Direct => "direct",
        LinkKind::Relayed => "relayed",
        LinkKind::Queued => "queued",
    }
}

type Out = Arc<Mutex<tokio::io::Stdout>>;

async fn emit(out: &Out, v: Value) {
    let mut line = v.to_string();
    line.push('\n');
    let mut o = out.lock().await;
    let _ = o.write_all(line.as_bytes()).await;
    let _ = o.flush().await;
}

async fn serve(a: Args) -> Result<()> {
    let target = a
        .target
        .ok_or_else(|| anyhow!("--target is required\n{USAGE}"))?;
    let node = Node::builder(config("Burrow", &a))
        .serve_tcp(target)
        .spawn()
        .await?;
    let out: Out = Arc::new(Mutex::new(tokio::io::stdout()));
    emit(&out, json!({"ready": true, "id": node.id().to_string()})).await;

    let mut lines = BufReader::new(tokio::io::stdin()).lines();
    while let Some(line) = lines.next_line().await? {
        if line.trim().is_empty() {
            continue;
        }
        let cmd: Cmd = match serde_json::from_str(&line) {
            Ok(c) => c,
            Err(e) => {
                emit(&out, json!({"error": format!("bad command: {e}")})).await;
                continue;
            }
        };
        let node = node.clone();
        let out = out.clone();
        tokio::spawn(async move {
            let id = cmd.id;
            let mut reply = match handle(&node, cmd).await {
                Ok(v) => v,
                Err(e) => json!({"error": format!("{e:#}")}),
            };
            reply["id"] = json!(id);
            emit(&out, reply).await;
        });
    }
    // Burrow closed our stdin: it stopped, or the feature was turned off.
    node.shutdown().await
}

async fn handle(node: &Node, cmd: Cmd) -> Result<Value> {
    match cmd.op.as_str() {
        // One device, one use, a short life. The tag ties the peer to that device, so
        // revoking the device on Burrow removes it here too.
        "invite" => {
            if cmd.tag.is_empty() {
                bail!("invite needs a tag");
            }
            let ttl = Duration::from_secs(cmd.ttl.clamp(60, 3600));
            let inv = node.invite_tagged(ttl, 1, vec![cmd.tag]);
            Ok(json!({"invite": inv.encode()}))
        }
        "forget" => {
            let mut n = 0;
            for p in node.peers() {
                if p.tags.contains(&cmd.tag) && node.remove_peer(p.id)? {
                    n += 1;
                }
            }
            Ok(json!({"removed": n}))
        }
        "status" => {
            let peers: Vec<PeerOut> = node
                .peers()
                .into_iter()
                .map(|p| PeerOut {
                    id: p.id.to_string(),
                    link: node.link(p.id).map(|(l, _)| link_name(l)),
                    name: p.name,
                    tags: p.tags,
                })
                .collect();
            let online = node.online(Duration::from_millis(200)).await;
            Ok(json!({"id": node.id().to_string(), "online": online, "peers": peers}))
        }
        op => bail!("unknown op {op}"),
    }
}

#[derive(Deserialize)]
struct ConnectIn {
    #[serde(default)]
    invite: String,
    #[serde(default)]
    peer: String,
}

async fn connect(a: Args) -> Result<()> {
    let host = std::env::var("HOSTNAME").unwrap_or_else(|_| "computer".into());
    let node = Node::spawn(config(&host, &a)).await?;
    let mut lines = BufReader::new(tokio::io::stdin()).lines();
    let first = lines
        .next_line()
        .await?
        .ok_or_else(|| anyhow!("nothing on stdin"))?;
    let input: ConnectIn = serde_json::from_str(&first).context("stdin")?;
    let peer: NodeId = if !input.invite.is_empty() {
        node.join(input.invite.trim()).await.context("join")?.id
    } else {
        input.peer.parse().map_err(|e| anyhow!("peer id: {e}"))?
    };
    let fwd = node.forward_tcp(peer, a.listen).await?;
    let out: Out = Arc::new(Mutex::new(tokio::io::stdout()));
    emit(
        &out,
        json!({"port": fwd.local_addr().port(), "peer": peer.to_string()}),
    )
    .await;
    while lines.next_line().await?.is_some() {}
    drop(fwd);
    node.shutdown().await
}
