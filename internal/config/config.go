// Copyright (c) 2026 Pedro Datorre
// SPDX-License-Identifier: MPL-2.0

// Package config holds the provider configuration shared with resources.
package config

import "time"

// Config is populated by the provider's Configure method and read by resources
// at request time.
type Config struct {
	Endpoint           string
	Timeout            time.Duration
	BootTimeout        time.Duration
	InsecureSkipVerify bool
}
