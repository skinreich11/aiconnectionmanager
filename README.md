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
RABBITMQ_DEFAULT_USER=dev \
RABBITMQ_DEFAULT_PASS='change-me-locally' \
./docker-run.sh
```

The script builds `aiconnectionmanager:local`, replaces the previous local
Compose stack, starts RabbitMQ, Valkey, and the service, and publishes the
secure WebSocket service at `wss://127.0.0.1:8443/ws`. RabbitMQ is also
available at `amqp://localhost:5672` and its management API at
`http://localhost:15672`. Valkey is available at `redis://localhost:6379`.
The application container starts with `go run . server` and generates its TLS
certificate and private key inside the container.

The script supports these optional environment variables:

```sh
RABBITMQ_DEFAULT_USER=dev RABBITMQ_DEFAULT_PASS='change-me-locally' \
HOST_PORT=9443 VALKEY_HOST_PORT=6380 IMAGE_NAME=aiconnectionmanager:dev CONTAINER_NAME=aiconnectionmanager-dev \
./docker-run.sh
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

The server supports `-addr`, `-cert`, `-key`, `-instance-id`, `-interval`, `-amqp-url`, `-amqp-username`, `-amqp-queue-prefix`, `-ecs-metadata-url`, `-valkey-url`, `-valkey-username`, and the local-only `-container-id` override; the client supports `-url`, `-ca`, and `-client-id`.

## ECS production connectivity

When `ECS_CONTAINER_METADATA_URI_V4` is present, the service treats the process as an ECS deployment and requires both external services before it starts. It does not start or fall back to local RabbitMQ or Valkey. Configure the ECS task with the Amazon MQ broker endpoint and ElastiCache endpoint:

```text
AMQP_URL=amqps://<amazon-mq-broker>:5671/%2F
VALKEY_URL=rediss://<elasticache-valkey-endpoint>:6379
```

Inject `AMQP_USERNAME`, `AMQP_PASSWORD`, `VALKEY_USERNAME`, and `VALKEY_PASSWORD` through the ECS task definition's Secrets Manager `secrets` entries. Do not set `AMQP_CONTAINER_ID` in ECS; the service reads the task's `DockerId` from `ECS_CONTAINER_METADATA_URI_V4` and uses that same ID for RabbitMQ and Valkey. The ECS task security group must be able to reach Amazon MQ on AMQP/TLS port `5671` and the ElastiCache endpoint on its configured TLS port.

The service validates `amqps://` and `rediss://` schemes in ECS mode, pings Valkey during startup, and connects RabbitMQ using `AMQP_URL`. Amazon MQ broker endpoints and ElastiCache endpoints are supplied by deployment configuration, not provisioned by this repository.

## Amazon MQ RabbitMQ consumer

The service declares and consumes this exclusive, auto-delete queue before starting its WebSocket server:

The service declares and consumes this exclusive, auto-delete queue before starting its WebSocket server:

```text
prod.connection.aiconnectionmanager.send-request.{DockerId}.q.
```

Every message is logged as `event received: <message body>` and acknowledged after logging. The queue is intentionally task-specific, so each horizontally scaled ECS task receives a different queue. Producers must publish or route messages to the desired task queues.

After a WebSocket connection is accepted, the service writes a Valkey mapping with the client ID as the key and the same container ID used in the RabbitMQ queue name as the value. The mapping is deleted when that WebSocket terminates; a reconnect writes it again.

For local development, set RabbitMQ credentials and start the included broker and service:

```sh
export RABBITMQ_DEFAULT_USER=dev
export RABBITMQ_DEFAULT_PASS='change-me-locally'
docker compose up --build
```

`AMQP_CONTAINER_ID=local-compose` is used only by the Compose setup because ECS metadata is unavailable locally. Do not use that override in ECS.

### Inspect local Valkey mappings

The Compose service exposes Valkey on port `6379` by default. Use the bundled CLI from the Valkey container:

```sh
# Set these in the shell used for each `docker compose` command.
export RABBITMQ_DEFAULT_USER=dev
export RABBITMQ_DEFAULT_PASS='change-me-locally'

docker compose exec valkey valkey-cli --scan
docker compose exec valkey valkey-cli get terminal-1
```

To create a mapping from a local test client, run this in another terminal while the stack is up:

```sh
docker compose exec aiconnectionmanager \
  go run . client -url wss://127.0.0.1:8443/ws -ca configs/cert.pem -client-id terminal-1
```

Or connect from the host with a locally installed `valkey-cli`/`redis-cli`:

```sh
valkey-cli -h 127.0.0.1 -p 6379 --scan
valkey-cli -h 127.0.0.1 -p 6379 get terminal-1
```

For the example client ID above, the value should be `local-compose`:

```text
terminal-1
local-compose
```

### Send a local RabbitMQ event

Start the stack and wait until the service logs that it subscribed to the queue:

```sh
export RABBITMQ_DEFAULT_USER=dev
export RABBITMQ_DEFAULT_PASS='change-me-locally'
./docker-run.sh
```

In a second terminal, publish through RabbitMQ's management API. The local Compose setup uses this queue name:

```sh
export QUEUE_NAME='prod.connection.aiconnectionmanager.send-request.local-compose.q.'

curl --fail --user "$RABBITMQ_DEFAULT_USER:$RABBITMQ_DEFAULT_PASS" \
  --header 'content-type: application/json' \
  --request POST \
  --data "{\"properties\":{},\"routing_key\":\"$QUEUE_NAME\",\"payload\":\"{\\\"requestId\\\":\\\"local-1\\\",\\\"message\\\":\\\"hello from RabbitMQ\\\"}\",\"payload_encoding\":\"string\"}" \
  http://localhost:15672/api/exchanges/%2F/amq.default/publish
```

The response should contain `"routed":true`, and the application terminal should log:

```text
event received: {"requestId":"local-1","message":"hello from RabbitMQ"}
```

To inspect the queues through the management API:

```sh
curl --fail --user "$RABBITMQ_DEFAULT_USER:$RABBITMQ_DEFAULT_PASS" \
  http://localhost:15672/api/queues/%2F
```
