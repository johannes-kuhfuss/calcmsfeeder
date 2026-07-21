package main

import (
	"log"

	"github.com/johannes-kuhfuss/calcmsfeeder/app"
)

func main() {
	if err := app.RunApp(); err != nil {
		log.Fatal(err)
	}
}
