# godns

## Building

Builds binaries for `darwin_arm64`, `linux_amd64`, and `linux_arm64`.

```shell
./build.sh
```

## Configuration

Modify the [hosts.json](https://github.com/nodesocket/godns/blob/master/hosts.json) config file with keys => values of hosts => ips.

The default fallback resolver is [Cloudflare public DNS](https://developers.cloudflare.com/1.1.1.1/) _(1.1.1.1)_ if no matching host is found in `hosts.json`.

## Usage

```shell
dig @127.0.0.1 app1.mydomain.com

; <<>> DiG 9.10.6 <<>> @127.0.0.1 app1.mydomain.com
; (1 server found)
;; global options: +cmd
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 42430
;; flags: qr aa rd; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 0
;; WARNING: recursion requested but not available

;; QUESTION SECTION:
;app1.mydomain.com.     IN  A

;; ANSWER SECTION:
app1.mydomain.com.  1   IN  A   1.2.3.4

;; Query time: 0 msec
;; SERVER: 127.0.0.1#53(127.0.0.1)
;; WHEN: Thu Apr 10 10:21:05 CDT 2025
;; MSG SIZE  rcvd: 68
```
