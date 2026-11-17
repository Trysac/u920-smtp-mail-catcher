package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Trysac/u920-smtp-mail-catcher"
)

// Run executes the CLI command and returns an exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("smtp-mail-catcher", flag.ContinueOnError)
	fs.SetOutput(stderr)
	smtpAddr := fs.String("smtp", "127.0.0.1:1025", "SMTP listen address")
	httpAddr := fs.String("http", "127.0.0.1:1080", "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	smtpListener, err := net.Listen("tcp", *smtpAddr)
	if err != nil {
		fmt.Fprintf(stderr, "error listening on smtp %s: %v\n", *smtpAddr, err)
		return 2
	}
	defer smtpListener.Close()

	httpListener, err := net.Listen("tcp", *httpAddr)
	if err != nil {
		fmt.Fprintf(stderr, "error listening on http %s: %v\n", *httpAddr, err)
		return 2
	}
	defer httpListener.Close()

	srv := smtpmailcatcher.NewServer(smtpmailcatcher.Config{})
	defer srv.Close()

	httpServer := &http.Server{
		Handler: srv.HTTPHandler(),
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- srv.Serve(smtpListener)
	}()
	go func() {
		errCh <- httpServer.Serve(httpListener)
	}()

	fmt.Fprintf(stdout, "smtp-mail-catcher running on SMTP %s and HTTP %s\n", *smtpAddr, *httpAddr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case <-sigCh:
		_ = httpServer.Close()
		_ = srv.Close()
		return 0
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(stderr, "server error: %v\n", err)
			return 2
		}
		return 0
	}
}

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}
