// package app ties together all bits and pieces to start the program
package app

import (
	"flag"

	"github.com/johannes-kuhfuss/calcmsfeeder/config"
)

var (
	cfg config.AppConfig
	//calCmsService  service.DefaultCalCmsService
)

const ()

// RunApp orchestrates the startup of the application
func RunApp() {
	getCmdLine()
	err := config.InitConfig(config.EnvFile, &cfg)
	if err != nil {
		panic(err)
	}
	wireApp()
}

// getCmdLine checks the command line arguments
func getCmdLine() {
	flag.StringVar(&config.EnvFile, "config.file", ".env", "Specify location of config file. Default is .env")
	flag.Parse()
}

// wireApp initializes the services in the right order and injects the dependencies
func wireApp() {
	//calCmsService = service.NewCalCmsService(&cfg, &fileRepo)
}
