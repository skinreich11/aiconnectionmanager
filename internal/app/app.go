package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"aiconnectionmanager/internal/client"
	"aiconnectionmanager/internal/server"
)

// Run dispatches the command-line entry point and returns a process exit code.
func Run(arguments []string, output, errorsOutput io.Writer) int {
	if len(arguments) == 0 {
		printUsage(errorsOutput)
		return 2
	}

	var err error
	switch arguments[0] {
	case "server":
		err = runServerCommand(arguments[1:], errorsOutput)
	case "client":
		err = runClientCommand(arguments[1:], output, errorsOutput)
	case "help", "-h", "--help":
		printUsage(output)
		return 0
	default:
		printUsage(errorsOutput)
		err = fmt.Errorf("unknown command %q", arguments[0])
	}

	if err != nil {
		fmt.Fprintln(errorsOutput, err)
		return 1
	}
	return 0
}

func runServerCommand(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	flags.SetOutput(output)
	address := flags.String("addr", "127.0.0.1:8443", "address for the secure WebSocket service")
	certificatePath := flags.String("cert", "configs/cert.pem", "TLS certificate path; generated when absent")
	keyPath := flags.String("key", "configs/key.pem", "TLS private key path; generated when absent")
	instanceID := flags.String("instance-id", defaultInstanceID(), "unique ID for this service instance")
	interval := flags.Duration("interval", time.Minute, "time between greeting messages")
	if err := flags.Parse(arguments); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := log.New(output, "server: ", log.LstdFlags)
	return server.Run(ctx, server.Config{
		Address:         *address,
		CertificatePath: *certificatePath,
		KeyPath:         *keyPath,
		InstanceID:      *instanceID,
		Interval:        *interval,
		ReportInterval:  30 * time.Second,
	}, logger)
}

func runClientCommand(arguments []string, output, errorsOutput io.Writer) error {
	flags := flag.NewFlagSet("client", flag.ContinueOnError)
	flags.SetOutput(errorsOutput)
	websocketURL := flags.String("url", "wss://127.0.0.1:8443/ws", "secure WebSocket service URL")
	certificatePath := flags.String("ca", "configs/cert.pem", "CA certificate used to verify the service")
	clientID := flags.String("client-id", "", "optional client ID to register with the service")
	if err := flags.Parse(arguments); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return client.RunWithID(ctx, *websocketURL, *certificatePath, *clientID, output)
}

func defaultInstanceID() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "instance"
	}
	var builder strings.Builder
	for _, character := range hostname {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._-", character) {
			builder.WriteRune(character)
			continue
		}
		builder.WriteByte('-')
	}
	return fmt.Sprintf("%s-%d", builder.String(), os.Getpid())
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  go run . server [flags]   Start the secure WebSocket service")
	fmt.Fprintln(output, "  go run . client [flags]   Connect from a terminal and print greetings")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "The server listens on 127.0.0.1:8443 and generates configs/cert.pem and configs/key.pem when needed.")
	fmt.Fprintln(output, "Each connection receives Hello World! every minute; active connections are reported every 30 seconds.")
}
