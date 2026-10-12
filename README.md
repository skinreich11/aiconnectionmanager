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
Compose stack, starts RabbitMQ plus the service, and publishes the secure
WebSocket service at `wss://127.0.0.1:8443/ws`. RabbitMQ is also available at
`amqp://localhost:5672` and its management API at `http://localhost:15672`.
The application container starts with `go run . server` and generates its TLS
certificate and private key inside the container.

The script supports these optional environment variables:

```sh
RABBITMQ_DEFAULT_USER=dev RABBITMQ_DEFAULT_PASS='change-me-locally' \
HOST_PORT=9443 IMAGE_NAME=aiconnectionmanager:dev CONTAINER_NAME=aiconnectionmanager-dev \
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

The server supports `-addr`, `-cert`, `-key`, `-instance-id`, `-interval`, `-amqp-url`, `-amqp-username`, `-amqp-queue-prefix`, `-ecs-metadata-url`, and the local-only `-container-id` override; the client supports `-url`, `-ca`, and `-client-id`.

## Amazon MQ RabbitMQ consumer

Set `AMQP_URL`, `AMQP_USERNAME`, and `AMQP_PASSWORD` in the ECS task definition to enable the request consumer. Use an `amqps://` Amazon MQ broker URL without credentials; inject the username and password from AWS Secrets Manager through ECS task secrets. ECS supplies `ECS_CONTAINER_METADATA_URI_V4`, which the service calls during startup to read the current container's `DockerId`.

The service declares and consumes this exclusive, auto-delete queue before starting its WebSocket server:

```text
prod.connection.aiconnectionmanager.send-request.{DockerId}.q.
```

Every message is logged as `event received: <message body>` and acknowledged after logging. The queue is intentionally task-specific, so each horizontally scaled ECS task receives a different queue. Producers must publish or route messages to the desired task queues.

For local development, set RabbitMQ credentials and start the included broker and service:

```sh
RABBITMQ_DEFAULT_USER=dev \
RABBITMQ_DEFAULT_PASS='change-me-locally' \
docker compose up --build
```

`AMQP_CONTAINER_ID=local-compose` is used only by the compose setup because ECS metadata is unavailable locally. Do not use that override in ECS.

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
