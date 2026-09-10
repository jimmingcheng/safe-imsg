package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jimmingcheng/safe-imsg/internal/broker"
	"github.com/jimmingcheng/safe-imsg/internal/config"
	"github.com/jimmingcheng/safe-imsg/internal/policy"
	"github.com/jimmingcheng/safe-imsg/internal/version"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Printf("safe-imsgd %s (%s)\n", version.Version, version.Commit)
		return 0
	}
	if len(args) == 0 {
		usage()
		return 2
	}
	command := args[0]
	if command == "config" {
		if len(args) < 2 || args[1] != "validate" {
			usage()
			return 2
		}
		args = append([]string{"validate"}, args[2:]...)
		command = "validate"
	}
	if command != "run" && command != "validate" {
		usage()
		return 2
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "absolute path to broker JSON config")
	if err := flags.Parse(args[1:]); err != nil || *configPath == "" || flags.NArg() != 0 {
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	server, err := broker.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := policy.Load(cfg.PolicyPath); err != nil {
		fmt.Fprintln(os.Stderr, "policy validation failed:", err)
		return 1
	}
	if command == "validate" {
		fmt.Println("configuration is valid")
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: safe-imsgd run --config PATH")
	fmt.Fprintln(os.Stderr, "       safe-imsgd config validate --config PATH")
	fmt.Fprintln(os.Stderr, "       safe-imsgd version")
}
