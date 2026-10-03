use rquickjs::function::Func;
use rquickjs::{Ctx, Function, Runtime};
fn dns_resolve(_h: String) -> String { "93.184.216.34".into() }
fn main() {
    let pre = std::fs::read_to_string("spike/pac/pacutils.js").unwrap();
    let pac = std::fs::read_to_string("spike/pac/test.pac").unwrap();
    let rt = Runtime::new().unwrap();
    let c1 = rquickjs::Context::full(&rt).unwrap();
    c1.with(|c: Ctx| { let _: () = c.eval(pre.as_bytes()).unwrap(); let _: () = c.eval(pac.as_bytes()).unwrap(); println!("ctx1 ok"); });
    drop(c1); drop(rt);
    let rt2 = Runtime::new().unwrap();
    let c2 = rquickjs::Context::full(&rt2).unwrap();
    let r = c2.with(|c: Ctx| -> Result<String, rquickjs::Error> {
        c.globals().set("dnsResolve", Func::from(dns_resolve))?;
        println!("step: set dnsResolve ok");
        let _: () = c.eval(pre.as_bytes())?;
        println!("step: prelude ok");
        let _: () = c.eval(pac.as_bytes())?;
        println!("step: pac ok");
        let f: Function = c.globals().get("FindProxyForURL")?;
        f.call(("http://app.example/path", "app.example"))
    });
    match r { Ok(v) => println!("ctx2 ok: {v}"), Err(e) => println!("ctx2 failed at: {e:?}") }
}
