package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/26000/mahou/config"
	"github.com/26000/mahou/matrix"
)

func main() {
	path := flag.String("c", TgMXIDConf.PATH, "path to your configuration "+
		"file (will be created if not exists)")
	flag.Parse()

	logger := log.New(os.Stdout, "mahou ", log.LstdFlags)
	logger.Printf("v%v is booting up! https://github.com/26000/mahou\n",
		TgMXIDConf.VERSION)

	if !fileExists(*path) {
		err := os.MkdirAll(filepath.Dir(*path), os.FileMode(0700))
		if err != nil {
			logger.Fatalf("unable to create the configuration file"+
				"directory: %v\n", err)
		}

		err = TgMXIDConf.PopulateConfig(*path)
		if err != nil {
			logger.Fatalf("could not create a configuration file: "+
				"%v\n", err)
		}

		logger.Printf("a configuration file was created at %v; insert "+
			"your settings there and launch mahou again\n", *path)
		os.Exit(0)
	}

	conf, err := maConf.ReadConfig(*path)
	if err != nil {
		logger.Fatalf("error reading config: %v\n", err)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go matrix.Launch(conf, *path)
	wg.Wait()
}

// fileExists returns true if the file exists.
func fileExists(file string) bool {
	_, err := os.Stat(file)
	return err == nil
}
