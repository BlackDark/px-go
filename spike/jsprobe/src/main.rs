// Spike: can rquickjs host px-go's PAC prelude + a PAC file?
// Loads internal/pac/pacutils.go's 140-line JS prelude verbatim (copied to
// ../pac/pacutils.js) plus a representative PAC, then calls
// FindProxyForURL(url, host). rquickjs 0.9 scopes work inside Context::with().
use rquickjs::function::Func;
use rquickjs::{Ctx, Function, Runtime};

static DNS_CALLS: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);

#[rquickjs::function]
fn dns_resolve(_host: String) -> String {
    // px-go injects a real resolver here; the stub just proves the Rust callback
    // boundary works from JS.
    DNS_CALLS.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    "93.184.216.34".to_string()
}
use std::time::Instant;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let prelude = std::fs::read_to_string(concat!(env!("CARGO_MANIFEST_DIR"), "/../pac/pacutils.js"))?;
    let pac = std::fs::read_to_string(concat!(env!("CARGO_MANIFEST_DIR"), "/../pac/test.pac"))?;

    let rt = Runtime::new()?;
    let ctx = rquickjs::Context::full(&rt)?;

    let _keep = (); let out = ctx.with(|c: Ctx| -> Result<String, rquickjs::Error> {
        // px-go injects dnsResolve from Go; stub it (the test PAC passes a
        // literal IP to isInNet so the stub should not be called).
        c.globals().set("dnsResolve", Func::from(dns_resolve))?;

        let t0 = Instant::now();
        let _: () = c.eval(prelude.as_bytes())?;
        let t_prelude = t0.elapsed();
        let t1 = Instant::now();
        let _: () = c.eval(pac.as_bytes())?;
        let t_pac = t1.elapsed();
        println!("prelude eval: {:?}   pac eval: {:?}", t_prelude, t_pac);

        let f: Function = c.globals().get("FindProxyForURL")?;
        let cases: Vec<(&str, &str, &str)> = vec![
            ("http://app.example/path", "app.example", "default path"),
            ("ftp://files.example.com/x", "files.example.com", "shExpMatch(ftp://*)"),
            ("http://svc.example.com/special/thing", "svc.example.com", "shExpMatch(*/special/*)"),
            ("http://host.direct.example/", "host.direct.example", "dnsDomainIs(.direct.example)"),
            ("http://10.1.2.3/thing", "10.1.2.3", "isInNet(10.0.0.0/8)"),
            ("http://11.1.2.3/thing", "11.1.2.3", "isInNet negative -> default"),
        ];
        let mut lines = String::new();
        for (url, host, label) in &cases {
            let out: String = f.call((*url, *host))?;
            lines.push_str(&format!("{:<38}| {:<24}| {:<30}| {}\n", url, host, label, out));
        }

        // Rough per-call cost (informational only).
        let n = 20_000u32;
        let t = Instant::now();
        for _ in 0..n {
            let _: String = f.call(("http://app.example/path", "app.example"))?;
        }
        lines.push_str(&format!(
            "per-call: {:.2} us (n={})\n",
            t.elapsed().as_secs_f64() / n as f64 * 1e6,
            n
        ));
        Ok(lines)
    })?;
    print!("{out}");

    // Hot-reload shape (fresh Runtime+Context per PAC, as px-go does on reload)
    // lives in spike/dbg: it worked there when the first Runtime was dropped
    // before the second was created, but kept failing here. Not chased further;
    // see spike/README.md.
    println!(
        "dnsResolve callback invocations: {}",
        DNS_CALLS.load(std::sync::atomic::Ordering::Relaxed)
    );
    Ok(())
}
