# AIGateway secure WebSocket service

Start the service from the repository directory:

```sh
go run . server
```

On first start, it creates a local TLS certificate at `configs/cert.pem` and a private key at `configs/key.pem` (`key.pem` is written with owner-only permissions). The service listens on `wss://127.0.0.1:8443/ws`.

In another terminal on the same machine, connect with:

```sh
go run . client
```

The client verifies the service certificate using `configs/cert.pem` and prints `Hello World!` once per minute until interrupted with `Ctrl-C`.

Each connection receives a generated client ID. To provide a stable ID, use `-client-id`:

```sh
go run . client -client-id terminal-1
```

The server keeps a thread-safe in-memory registry per service instance and logs every active client ID, endpoint, port, and WebSocket pointer every 30 seconds. Use a unique `-instance-id` for each horizontally scaled service instance.

To build a standalone command:

```sh
go build -o aigateway .
./aigateway server
./aigateway client
```

The canonical command package can also be run directly:

```sh
go run ./cmd/worker server
```

The server supports `-addr`, `-cert`, `-key`, `-instance-id`, and `-interval`; the client supports `-url`, `-ca`, and `-client-id`.
