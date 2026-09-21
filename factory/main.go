// Command factory is the service that works the issues routed to it unattended, on a host of its
// own. It is a second driver over the same worker pipeline a local claim starts, and it shares
// nothing with a developer's machine but GitHub.
//
// So far it runs in fake mode only: a canned queue and scripted workers, which need no tokens, no
// git and no GitHub. What it does is read over its HTTP interface, which never writes anything.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	if len(os.Args) > 1 && os.Args[1] == "scripted-worker" {
		os.Exit(scriptedWorker(os.Args[2:], os.Stdout, os.Stderr))
	}
	config := flag.String("config", "factory.json", "configuration file")
	fake := flag.Bool("fake", false, "canned queue and scripted workers: no tokens, no git, no GitHub")
	paused := flag.Bool("paused", false, "show the queue and start nothing")
	flag.Parse()
	if err := run(*config, *fake, *paused); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(config string, fake, paused bool) error {
	settings, err := Load(config)
	if err != nil {
		return err
	}
	settings.Paused = settings.Paused || paused
	if !fake {
		return fmt.Errorf("only fake mode runs so far; start it with -fake (working routed issues for real arrives with the tickets that connect GitHub)")
	}

	// Listening comes before everything else: a second factory on this host has to fail here, before
	// it has started a run or taken an issue from anybody.
	listener, err := net.Listen("tcp", settings.Listen)
	if err != nil {
		return fmt.Errorf("%w; is another factory running on this host? one host runs one factory", err)
	}
	defer listener.Close()

	factory, err := New(settings, fake)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: factory.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("the HTTP interface stopped: %v", err)
		}
	}()
	log.Printf("factory on http://%s (fake=%v paused=%v label=%s deadline=%s data=%s)",
		settings.Listen, fake, settings.Paused, settings.Label, settings.Deadline, settings.DataDir)

	// On SIGTERM the factory ends the worker it is running before it exits: no worker process is left
	// behind on the host, and the run is recorded as interrupted.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	factory.Work(ctx)
	log.Printf("stopping")

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}
