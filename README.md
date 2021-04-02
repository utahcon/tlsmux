# tlsmux

## What is `tlsmux`?

## What makes `tlsmux` special? 

* automatic HTTP/HTTPS redirect (optional)
* dynamically configured
* hotswap configuration
* persistent configuration
* self-service backend registration
* ACME protocol support
* backend health checks

`tlsmux` can be statically configured, or dynamically configured. With an API exposed back end clients can self register
their frontends, or configure themselves as additional backends for an existing frontend.

## Configuration

```yaml
port: 443
```