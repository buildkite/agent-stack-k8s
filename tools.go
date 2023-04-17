//go:build tools

package main

import (
	_ "github.com/Khan/genqlient"
	_ "github.com/golang/mock/mockgen"
	_ "gotest.tools/gotestsum"
)
