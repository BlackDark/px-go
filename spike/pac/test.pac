// PAC test fixture for the rquickjs spike. Exercises shExpMatch, dnsDomainIs
// and isInNet plus a plain default path.
function FindProxyForURL(url, host) {
  if (shExpMatch(url, "ftp://*")) return "DIRECT";
  if (dnsDomainIs(host, ".direct.example")) return "DIRECT";
  if (shExpMatch(url, "*/special/*")) return "PROXY special.example.com:8080";
  if (isInNet(host, "10.0.0.0", "255.0.0.0")) return "PROXY internal.example.com:3128";
  return "PROXY default.example.com:8080";
}