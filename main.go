// Copyright (c) 2026 Pedro Datorre
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/pedrodatorre/terraform-provider-truenas-installer/internal/provider"
)

// version is set at build time by goreleaser (-X main.version=...).
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with debugger support")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/pedrodatorre/truenas-installer",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
