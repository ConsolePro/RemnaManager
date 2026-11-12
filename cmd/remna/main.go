package main

import (
	"context"
	"errors"
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
	inspect := flag.String("inspect-manager", "", "inspect Go manager build identity without executing it")
	demo := flag.Bool("demo", false, "simulate installation without changing the host")
	flag.Parse()
	if *inspect != "" {
		if !updater.IsManagerExecutable(*inspect) {
			os.Exit(1)
		}
		fmt.Println("RemnaManager Go executable")
		return
	}
	if *version {
		fmt.Println(updater.Version + " (Go TUI preview)")
		return
	}
	if err := app.RunMode(context.Background(), *path, *demo); err != nil {
		if errors.Is(err, app.ErrRestart) {
			err = updater.Restart()
		}
		if err == nil {
			return
		}
		fmt.Fprintln(os.Stderr, "remanager:", err)
		os.Exit(1)
	}
}
