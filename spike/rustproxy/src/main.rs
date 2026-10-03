// Minimal HTTP forward proxy + CONNECT tunnel for a Go-vs-Rust throughput/RSS spike.
// Scope: forward proxying of absolute-URI requests and a bidirectional CONNECT relay.
// Deliberately no auth, no PAC, no config, no Windows code.
use std::sync::Arc;
use tokio::io::{AsyncBufReadExt, AsyncReadExt, AsyncWrite, AsyncWriteExt, BufReader};

// Duplicate a tokio socket into two owned handles (tokio TcpStream has no clone).
fn dup(t: TcpStream) -> std::io::Result<(TcpStream, TcpStream)> {
    let s = t.into_std()?;
    let b = s.try_clone()?;
    Ok((TcpStream::from_std(s)?, TcpStream::from_std(b)?))
}
use tokio::net::{TcpListener, TcpStream};

fn arg(name: &str, default: &str) -> String {
    let a = format!("--{}", name);
    let v: Vec<String> = std::env::args().collect();
    for w in v.windows(2) {
        if w[0] == a {
            return w[1].clone();
        }
    }
    default.to_string()
}

fn main() -> std::io::Result<()> {
    let addr = arg("listen", "127.0.0.1:18080");
    let workers: usize = arg("workers", "0").parse().unwrap_or(0);
    let rt = if workers > 0 {
        tokio::runtime::Builder::new_multi_thread()
            .worker_threads(workers)
            .enable_all()
            .build()?
    } else {
        tokio::runtime::Builder::new_multi_thread().enable_all().build()?
    };
    rt.block_on(async move {
        let l = TcpListener::bind(&addr).await?;
        eprintln!("rustproxy listening on {}", addr);
        let b = Arc::new(l);
        loop {
            let (sock, _peer) = b.accept().await?;
            sock.set_nodelay(true).ok();
            tokio::spawn(async move {
                let _ = serve(sock).await;
            });
        }
    })
}

async fn serve(sock: TcpStream) -> std::io::Result<()> {
    let mut reader = BufReader::with_capacity(16 * 1024, sock);
    let mut upstream: Option<(TcpStream, BufReader<TcpStream>)> = None;
    loop {
        let mut line = String::new();
        loop {
            line.clear();
            let n = reader.read_line(&mut line).await?;
            if n == 0 {
                return Ok(());
            }
            if !line.trim().is_empty() {
                break;
            }
        }
        let mut parts = line.trim_end().split(' ');
        let method = parts.next().unwrap_or("").to_string();
        let target = parts.next().unwrap_or("").to_string();
        let _ver = parts.next().unwrap_or("HTTP/1.1");

        let mut headers: Vec<(String, String)> = Vec::with_capacity(12);
        let mut content_length: usize = 0;
        let mut chunked_req = false;
        loop {
            let mut h = String::new();
            let n = reader.read_line(&mut h).await?;
            if n == 0 {
                return Ok(());
            }
            let h = h.trim_end_matches(['\r', '\n']);
            if h.is_empty() {
                break;
            }
            if let Some(p) = h.split_once(':') {
                let (k, v) = (p.0.trim().to_string(), p.1.trim().to_string());
                if k.eq_ignore_ascii_case("content-length") {
                    content_length = v.parse().unwrap_or(0);
                }
                if k.eq_ignore_ascii_case("transfer-encoding")
                    && v.to_ascii_lowercase().contains("chunked")
                {
                    chunked_req = true;
                }
                headers.push((k, v));
            }
        }

        if method.eq_ignore_ascii_case("CONNECT") {
            let mut s = TcpStream::connect(&target).await?;
            s.set_nodelay(true).ok();
            let raw = reader.get_mut();
            raw.write_all(b"HTTP/1.1 200 Connection established\r\n\r\n")
                .await?;
            raw.flush().await?;
            // Bytes may already sit in the read buffer; hand them to the tunnel.
            let pending: Vec<u8> = reader.buffer().to_vec();
            if !pending.is_empty() {
                reader.consume(pending.len());
                s.write_all(&pending).await?;
                s.flush().await?;
            }
            let (mut ca, mut cb) = dup(reader.into_inner())?;
            let (mut ua, mut ub) = dup(s)?;
            tokio::select! {
                _ = tokio::io::copy_bidirectional(&mut ca, &mut ub) => {}
                _ = tokio::io::copy_bidirectional(&mut ua, &mut cb) => {}
            }
            return Ok(());
        }

        // Forward proxying: absolute-URI request line.
        let (hostport, path) = match split_absolute(&target) {
            Some(v) => v,
            None => {
                reader
                    .get_mut()
                    .write_all(b"HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
                    .await?;
                return Ok(());
            }
        };

        if upstream.is_none() {
            match TcpStream::connect(&hostport).await {
                Ok(s) => {
                    s.set_nodelay(true).ok();
                    // tokio TcpStream is not Clone; duplicate the fd via std so the proxy can
// read responses on one handle and write requests on the other.
let (w, r) = dup(s)?;
upstream = Some((w, BufReader::with_capacity(16 * 1024, r)));
                }
                Err(_) => {
                    reader
                        .get_mut()
                        .write_all(b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n")
                        .await?;
                    return Ok(());
                }
            }
        }
        let up = upstream.as_mut().unwrap();
        let sock = &mut up.0;
        let up_reader = &mut up.1;

        (&mut *sock)
            .write_all(format!("{} {} HTTP/1.1\r\n", method, path).as_bytes())
            .await?;
        let mut head = String::with_capacity(256);
        for (k, v) in &headers {
            if k.eq_ignore_ascii_case("proxy-connection") || k.eq_ignore_ascii_case("connection") {
                continue;
            }
            head.push_str(k);
            head.push_str(": ");
            head.push_str(v);
            head.push_str("\r\n");
        }
        head.push_str("\r\n");
        sock.write_all(head.as_bytes()).await?;

        if content_length > 0 {
            let mut body = vec![0u8; content_length];
            reader.read_exact(&mut body).await?;
            sock.write_all(&body).await?;
        }
        sock.flush().await?;

        if !relay_response(up_reader, &mut reader.get_mut()).await? {
            return Ok(());
        }
        if chunked_req {
            return Ok(());
        }
    }
}



// Reads one upstream response, writes it verbatim to the client, and reports
// whether the client connection can be reused.
async fn relay_response<W: AsyncWrite + Unpin, R: tokio::io::AsyncRead + Unpin>(
    up: &mut BufReader<R>,
    client: &mut W,
) -> std::io::Result<bool> {
    let mut head: Vec<u8> = Vec::with_capacity(1024);
    let mut clen: Option<usize> = None;
    let mut chunked = false;
    loop {
        let mut byte = [0u8; 1];
        let n = up.read(&mut byte).await?;
        if n == 0 {
            return Ok(false);
        }
        head.push(byte[0]);
        if head.len() >= 4 && head[head.len() - 4..] == *b"\r\n\r\n" {
            for line in String::from_utf8_lossy(&head).lines() {
                let lower = line.to_ascii_lowercase();
                if let Some(v) = lower.strip_prefix("content-length:") {
                    clen = v.trim().parse().ok();
                }
                if let Some(v) = lower.strip_prefix("transfer-encoding:") {
                    if v.contains("chunked") {
                        chunked = true;
                    }
                }
            }
            let keep = clen.is_some() && !chunked;
            client.write_all(&head).await?;
            match clen {
                Some(l) => {
                    let mut remaining = l;
                    let mut buf = vec![0u8; 16 * 1024];
                    while remaining > 0 {
                        let want = remaining.min(buf.len());
                        let n = up.read(&mut buf[..want]).await?;
                        if n == 0 {
                            return Ok(false);
                        }
                        client.write_all(&buf[..n]).await?;
                        remaining -= n;
                    }
                }
                None => {
                    if chunked {
                        tokio::io::copy(up, client).await?;
                    }
                    return Ok(false);
                }
            }
            client.flush().await?;
            return Ok(keep);
        }
        if head.len() > 64 * 1024 {
            return Ok(false);
        }
    }
}

fn split_absolute(target: &str) -> Option<(String, String)> {
    let (scheme, rest) = target.split_once("://")?;
    if !scheme.eq_ignore_ascii_case("http") {
        return None;
    }
    let (authority, path) = match rest.find('/') {
        Some(i) => (&rest[..i], &rest[i..]),
        None => (rest, "/"),
    };
    if authority.contains('@') {
        return None;
    }
    let hostport = if authority.contains(':') {
        authority.to_string()
    } else {
        format!("{}:80", authority)
    };
    Some((hostport, path.to_string()))
}