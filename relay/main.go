package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// Parse command-line flags
	addr := flag.String("addr", ":9999", "Listen address (host:port)")
	tls := flag.Bool("tls", false, "Enable TLS")
	certFile := flag.String("cert", "", "TLS certificate file path")
	keyFile := flag.String("key", "", "TLS private key file path")
	flag.Parse()

	// Validate TLS flags
	if *tls && (*certFile == "" || *keyFile == "") {
		fmt.Fprintf(os.Stderr, "error: --cert and --key are required when --tls is enabled\n")
		os.Exit(1)
	}

	cfg := ServerConfig{
		Addr:     *addr,
		TLS:      *tls,
		CertFile: *certFile,
		KeyFile:  *keyFile,
	}

	server, err := NewRelayServer(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to initialize relay server: %v\n", err)
		os.Exit(1)
	}

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nreceived shutdown signal")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "error during shutdown: %v\n", err)
		}
	}()

	// Start the server
	if *tls {
		err = server.StartTLS(*certFile, *keyFile)
	} else {
		err = server.Start()
	}

	if err != nil && err.Error() != "http: Server closed" {
		fmt.Fprintf(os.Stderr, "error: relay server failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("relay server stopped")
}
