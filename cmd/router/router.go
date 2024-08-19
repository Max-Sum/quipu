package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Max-Sum/quipu/router"
	"github.com/akamensky/argparse"
)

func main() {
	parser := argparse.NewParser("quipu-router", "Routes connections based on sni")
	// Create string flag
	cfgpath := parser.String("c", "config", &argparse.Options{Help: "Path to config file"})
	// Parse input
	err := parser.Parse(os.Args)
	if err != nil {
		// In case of error print error and print usage
		// This can also be done by passing -h or --help flags
		fmt.Print(parser.Usage(err))
		os.Exit(1)
	}

	cfg, err := router.LoadAllConfsFromEnv()
	if err != nil && len(*cfgpath) == 0 {
		log.Fatalf("%v", err)
		os.Exit(2)
	}

	if len(*cfgpath) > 0 {
		cfg, err = router.LoadAllConfsFromIni(*cfgpath)
		if err != nil {
			log.Fatalf("%v", err)
			os.Exit(2)
		}
	}

	if cfg.ListenPlain == "" && cfg.ListenTLS == "" {
		fmt.Print(errors.New("listen address is missing"))
		os.Exit(1)
	}

	var plainServer *router.TCPServer
	var tlsServer *router.TCPServer
	errCh := make(chan error)
	if cfg.ListenPlain != "" {
		go func() {
			plainServer = router.NewTCPServer(cfg.ListenPlain, false, cfg)
			err = plainServer.ListenAndServe()
			if err != nil {
				errCh <- err
			}
		}()
	}
	if cfg.ListenTLS != "" {
		go func() {
			tlsServer = router.NewTCPServer(cfg.ListenTLS, true, cfg)
			err = tlsServer.ListenAndServe()
			if err != nil {
				errCh <- err
			}
		}()
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	select {
	case <-errCh:
		fmt.Print(err)
		os.Exit(1)
	case <-c:
	}
	// graceful exit
	if plainServer != nil {
		plainServer.Shutdown()
	}
	if tlsServer != nil {
		tlsServer.Shutdown()
	}
}
