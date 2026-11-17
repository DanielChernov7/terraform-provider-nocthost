package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/DanielChernov7/terraform-provider-nocthost/internal/provider"
)

// Generate docs with: go generate ./...
//go:generate go tool tfplugindocs generate --provider-name nocthost

// version is set by goreleaser.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/danielchernov7/nocthost",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
