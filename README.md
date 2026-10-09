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

## Docker

Build and run the server with Docker Desktop:

```sh
./docker-run.sh
```

The script builds `aiconnectionmanager:local`, replaces any previous container
with the same name, and publishes the secure WebSocket service at
`wss://127.0.0.1:8443/ws`. The container starts with `go run . server` and
generates its TLS certificate and private key inside the container.

The script supports these optional environment variables:

```sh
HOST_PORT=9443 IMAGE_NAME=aiconnectionmanager:dev CONTAINER_NAME=aiconnectionmanager-dev ./docker-run.sh
```

## Quality checks

The GitHub Actions pipeline delegates its shell commands to the Makefile. Run
the same checks locally with:

```sh
make quality
```

This runs formatting, `go vet`, tests with the race detector, coverage,
Staticcheck, golangci-lint, gosec, govulncheck, GitHub Actions linting,
and PMD CPD. PMD does not have Go rules; its CPD duplicate detector does
support Go. Java, curl, and unzip are needed for the PMD check.

The CI aggregate also runs Hadolint against the Dockerfile and builds and
smoke-tests the image:

```sh
make ci
```

Docker is needed for the Hadolint fallback when the `hadolint` executable is
not present.

SonarQube consumes `coverage.out` and enforces its quality gate in GitHub
Actions. Configure the `SONAR_TOKEN` repository secret and, depending on your
SonarQube deployment, the `SONAR_HOST_URL`, `SONAR_ROOT_CERT`,
`SONAR_PROJECT_KEY`, and `SONAR_ORGANIZATION` repository variables.

The canonical command package can also be run directly:

```sh
go run ./cmd/worker server
```

The server supports `-addr`, `-cert`, `-key`, `-instance-id`, and `-interval`; the client supports `-url`, `-ca`, and `-client-id`.
