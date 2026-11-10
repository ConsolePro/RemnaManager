package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/ConsolePro/RemnaManager/internal/app"
	"github.com/ConsolePro/RemnaManager/internal/config"
	"github.com/ConsolePro/RemnaManager/internal/updater"
	"os"
)

func main() {
	path := flag.String("config", config.DefaultPath, "configuration file")
	version := flag.Bool("version", false, "print preview version")
	demo := flag.Bool("demo", false, "simulate installation without changing the host")
	flag.Parse()
	if *version {
		fmt.Println(updater.Version + " (Go TUI preview)")
		return
	}
	if err := app.RunMode(context.Background(), *path, *demo); err != nil {
		fmt.Fprintln(os.Stderr, "remna:", err)
		os.Exit(1)
	}
}
