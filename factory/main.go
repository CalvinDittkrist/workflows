// Command factory is the service that works the issues routed to it unattended, on a host of its
// own. It is a second driver over the same worker pipeline a local claim starts, and it shares
// nothing with a developer's machine but GitHub.
//
// So far it works its line only in fake mode, with a canned queue and scripted workers that need no
// tokens, no git and no GitHub. Against real GitHub it runs paused: it clones the connected
// repositories, shows the live queue of routed issues and claims nothing. What it does is read over
// its HTTP interface, which never writes anything.
package main

import (
	"context"
	"errors"
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
	// Against real GitHub the factory only reads so far. Claiming an issue and starting a worker
	// arrives with its own ticket, and until then an unpaused start would promise work it cannot do.
	if !fake && !settings.Paused {
		return fmt.Errorf("a factory against real GitHub runs paused so far; start it with -paused or set \"paused\": true in %s (claiming routed issues arrives with the ticket that starts real workers)", config)
	}

	// On SIGTERM the factory ends the worker it is running before it exits: no worker process is left
	// behind on the host, and the run is recorded as interrupted. The clone of a connected repository
	// answers to it too, so a stop during a long clone is not waited out.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Listening comes before everything else: a second factory on this host has to fail here, before
	// it has started a run or taken an issue from anybody.
	listener, err := net.Listen("tcp", settings.Listen)
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf("%w; is another factory running on this host? one host runs one factory", err)
	}
	if err != nil {
		return fmt.Errorf("%w; listen names the address the factory answers on, such as %q", err, defaultListen)
	}
	defer listener.Close()
	// The address the kernel chose is the one that counts: a host that is not an IP literal can still
	// resolve to every interface, and this interface has no login of its own.
	if bound, ok := listener.Addr().(*net.TCPAddr); ok && bound.IP.IsUnspecified() {
		return fmt.Errorf("listen %q answers on every interface (%s); bind it to one address, such as %q, and reach it over the tailnet",
			settings.Listen, bound, defaultListen)
	}

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

	// The clones come after the interface answers and before the first poll: a worker branches off a
	// clone, so the host is made ready before there is work to give it. A first clone takes minutes,
	// which is why the interface is up while it runs and says it is connecting. Fake mode has nothing
	// to clone: its queue is canned.
	if !fake {
		factory.Connect(ctx)
	}
	factory.Work(ctx)
	log.Printf("stopping")

	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}
