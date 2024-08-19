package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Max-Sum/quipu/subscription"
	"github.com/akamensky/argparse"
)

func main() {
	parser := argparse.NewParser("quipu-subscription", "Chain proxies and provide a clash subscription file")
	// Create string flag
	cfgpath := parser.String("c", "config", &argparse.Options{Required: true, Help: "Path to config file"})
	// Parse input
	err := parser.Parse(os.Args)
	if err != nil {
		// In case of error print error and print usage
		// This can also be done by passing -h or --help flags
		fmt.Print(parser.Usage(err))
		os.Exit(1)
	}

	cfg, err := subscription.LoadAllConfsFromIni(*cfgpath)
	if err != nil {
		log.Fatalf("%v", err)
		os.Exit(2)
	}

	if cfg.Listen == "" {
		fmt.Print(errors.New("listen address is missing"))
		os.Exit(1)
	}

	var server *subscription.Server
	errCh := make(chan error)
	go func() {
		server = subscription.NewServer(cfg)
		err = server.ListenAndServe()
		if err != nil {
			errCh <- err
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	select {
	case <-errCh:
		fmt.Print(err)
		os.Exit(1)
	case <-c:
	}
	// graceful exit
	server.Shutdown(context.Background())
}
