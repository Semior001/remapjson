// Package cmd contains common command options and utilities
package cmd

import (
	"context"
	"fmt"
)

// CommonOpts contains common options for all commands
type CommonOpts struct {
	Context              context.Context
	ApplicationVersion   string
	ApplicationBuildDate string
}

// SetCommonOpts sets the common options
func (c *CommonOpts) SetCommonOpts(cc CommonOpts) {
	c.Context = cc.Context
	c.ApplicationVersion = cc.ApplicationVersion
	c.ApplicationBuildDate = cc.ApplicationBuildDate
}

// Version command prints app version
type Version struct{ CommonOpts }

// Execute prints app version
func (v Version) Execute([]string) error {
	fmt.Printf("bs2csv, version: %s, build date: %s\n", v.ApplicationVersion, v.ApplicationBuildDate)
	return nil
}
